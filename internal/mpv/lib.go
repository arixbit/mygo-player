package mpv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ffi"
)

// libFuncs holds the dynamically bound C functions of libmpv.
type libFuncs struct {
	ClientAPIVersion func() uint64                                                                                 `ffi:"mpv_client_api_version"`
	ErrorString      func(err int32) string                                                                        `ffi:"mpv_error_string"`
	Free             func(data unsafe.Pointer)                                                                     `ffi:"mpv_free"`
	Create           func() uintptr                                                                                `ffi:"mpv_create"`
	Initialize       func(ctx uintptr) int32                                                                       `ffi:"mpv_initialize"`
	TerminateDestroy func(ctx uintptr)                                                                             `ffi:"mpv_terminate_destroy"`
	SetOption        func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32                       `ffi:"mpv_set_option"`
	SetOptionString  func(ctx uintptr, name string, data string) int32                                             `ffi:"mpv_set_option_string"`
	Command          func(ctx uintptr, args **byte) int32                                                          `ffi:"mpv_command"`
	CommandAsync     func(ctx uintptr, replyUserdata uint64, args **byte) int32                                    `ffi:"mpv_command_async"`
	CommandString    func(ctx uintptr, args string) int32                                                          `ffi:"mpv_command_string"`
	CommandNode      func(ctx uintptr, args *Node, result *Node) int32                                             `ffi:"mpv_command_node"`
	SetProperty      func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32                       `ffi:"mpv_set_property"`
	SetPropertyStr   func(ctx uintptr, name string, data string) int32                                             `ffi:"mpv_set_property_string"`
	SetPropertyAsync func(ctx uintptr, replyUserdata uint64, name string, format int32, data unsafe.Pointer) int32 `ffi:"mpv_set_property_async"`
	GetProperty      func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32                       `ffi:"mpv_get_property"`
	GetPropertyStr   func(ctx uintptr, name string) unsafe.Pointer                                                 `ffi:"mpv_get_property_string"`
	FreeNodeContents func(node *Node)                                                                              `ffi:"mpv_free_node_contents"`
	ObserveProperty  func(ctx uintptr, replyUserdata uint64, name string, format int32) int32                      `ffi:"mpv_observe_property"`
	UnobserveProp    func(ctx uintptr, registeredReplyUserdata uint64) int32                                       `ffi:"mpv_unobserve_property"`
	RequestLogMsgs   func(ctx uintptr, minLevel string) int32                                                      `ffi:"mpv_request_log_messages"`
	WaitEvent        func(ctx uintptr, timeout float64) unsafe.Pointer                                             `ffi:"mpv_wait_event"`
	Wakeup           func(ctx uintptr)                                                                             `ffi:"mpv_wakeup"`

	RenderContextCreate            func(res *uintptr, ctx uintptr, params *RenderParam) int32 `ffi:"mpv_render_context_create"`
	RenderContextSetUpdateCallback func(rctx uintptr, callback uintptr, callbackCtx uintptr)  `ffi:"mpv_render_context_set_update_callback"`
	RenderContextUpdate            func(rctx uintptr) uint64                                  `ffi:"mpv_render_context_update"`
	RenderContextRender            func(rctx uintptr, params *RenderParam) int32              `ffi:"mpv_render_context_render"`
	RenderContextReportSwap        func(rctx uintptr)                                         `ffi:"mpv_render_context_report_swap"`
	RenderContextFree              func(rctx uintptr)                                         `ffi:"mpv_render_context_free"`
}

var (
	loaderMu   sync.Mutex
	envBindMu  sync.Mutex
	manifest   []byte
	packageDir string
	customPath string

	// loadMu serializes Load attempts. A failed attempt is not cached, so a
	// later Load (for example after SetLibraryPath or installing libmpv) can
	// retry. loaded/loadedPath are guarded by loadMu.
	loadMu     sync.Mutex
	loaded     bool
	loadedPath string
	fns        libFuncs
)

// Configure sets the embedded mygo-plugin.json manifest and package directory
// used by mygo/ffi to discover libmpv.
func Configure(m []byte, dir string) {
	loaderMu.Lock()
	manifest = m
	packageDir = dir
	loaderMu.Unlock()
}

// SetLibraryPath overrides the libmpv dynamic library path before Load is called.
func SetLibraryPath(path string) {
	loaderMu.Lock()
	customPath = path
	loaderMu.Unlock()
}

// LibraryPath resolves the libmpv dynamic library path using mygo/ffi lookup
// rules (MYGO_NATIVE_MPV, app resources, Frameworks, executable dir,
// .mygo/native, and mygo-plugin.json published builds), falling back to
// standard local development paths in unpackaged runs.
func LibraryPath() (string, error) {
	if !platformSupported {
		return "", fmt.Errorf("mpv: %s/%s: %w", runtime.GOOS, runtime.GOARCH, errors.ErrUnsupported)
	}
	loaderMu.Lock()
	p := customPath
	m := manifest
	dir := packageDir
	loaderMu.Unlock()

	if p != "" {
		return filepath.Abs(p)
	}
	lib := newFFILib(m, dir)
	path, err := lib.Path()
	if err == nil {
		return path, nil
	}
	if !mygo.App.IsPackaged() && os.Getenv(ffi.EnvVar("mpv")) == "" {
		if sysPath := findDevSystemLib(dir); sysPath != "" {
			return sysPath, nil
		}
	}
	return "", fmt.Errorf("mpv: %w (set %s=/path/to/%s or install libmpv)", err, ffi.EnvVar("mpv"), defaultLibFileName())
}

// Load loads libmpv and binds its C symbols. A successful load is cached for
// the lifetime of the process; a failed load is not, so callers may fix the
// environment (SetLibraryPath, MYGO_NATIVE_MPV, installing libmpv) and retry.
func Load() error {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded {
		return nil
	}
	path, bound, err := loadLibrary()
	if err != nil {
		return err
	}
	// Publish symbols only after a complete, successful bind so a failed
	// attempt can never leave fns partially populated.
	fns = bound
	loadedPath = path
	loaded = true
	return nil
}

func loadLibrary() (string, libFuncs, error) {
	var bound libFuncs
	if !platformSupported {
		return "", bound, fmt.Errorf("mpv: %s/%s: %w", runtime.GOOS, runtime.GOARCH, errors.ErrUnsupported)
	}
	loaderMu.Lock()
	p := customPath
	m := manifest
	dir := packageDir
	loaderMu.Unlock()

	if p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", bound, err
		}
		if err := bindPathWithFFI(abs, &bound); err != nil {
			return "", bound, err
		}
		return abs, bound, nil
	}

	lib := newFFILib(m, dir)
	path, err := lib.Path()
	if err == nil {
		if err := lib.Bind(&bound); err != nil {
			return "", bound, fmt.Errorf("mpv: %w", err)
		}
		return path, bound, nil
	}
	if !mygo.App.IsPackaged() && os.Getenv(ffi.EnvVar("mpv")) == "" {
		if sysPath := findDevSystemLib(dir); sysPath != "" {
			if err := bindPathWithFFI(sysPath, &bound); err != nil {
				return "", bound, err
			}
			return sysPath, bound, nil
		}
	}
	return "", bound, fmt.Errorf("mpv: %w (set %s=/path/to/%s or install libmpv)", err, ffi.EnvVar("mpv"), defaultLibFileName())
}

// LoadedPath returns the path of the loaded libmpv library, if Load succeeded.
// It is safe to call concurrently with Load.
func LoadedPath() string {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loadedPath
}

// errorTexts mirrors libmpv's mpv_error_string table so ErrorString stays
// readable even before libmpv is loaded.
var errorTexts = map[ErrorCode]string{
	ErrSuccess:             "success",
	ErrEventQueueFull:      "event queue full",
	ErrNoMem:               "memory allocation failed",
	ErrUninitialized:       "core not initialized",
	ErrInvalidParameter:    "invalid parameter",
	ErrOptionNotFound:      "option not found",
	ErrOptionFormat:        "unsupported format for accessing option",
	ErrOptionError:         "error setting option",
	ErrPropertyNotFound:    "property not found",
	ErrPropertyFormat:      "unsupported format for accessing property",
	ErrPropertyUnavailable: "property unavailable",
	ErrPropertyError:       "error accessing property",
	ErrCommand:             "error running command",
	ErrLoadingFailed:       "loading failed",
	ErrAOInitFailed:        "audio output initialization failed",
	ErrVOInitFailed:        "video output initialization failed",
	ErrNothingToPlay:       "no audio or video data played",
	ErrUnknownFormat:       "unrecognized file format",
	ErrUnsupported:         "not supported",
	ErrNotImplemented:      "operation not implemented",
	ErrGeneric:             "something happened",
}

// ErrorString returns a human-readable description of an mpv error code,
// preferring libmpv's own mpv_error_string when the library is loaded.
func ErrorString(code ErrorCode) string {
	loadMu.Lock()
	ok := loaded && fns.ErrorString != nil
	loadMu.Unlock()
	if ok {
		if s := fns.ErrorString(int32(code)); s != "" && s != "unknown error" {
			return s
		}
	}
	if s, ok := errorTexts[code]; ok {
		return s
	}
	return fmt.Sprintf("unknown error %d", int32(code))
}

// ClientAPIVersion returns libmpv's packed client API version ((major<<16)|minor).
func ClientAPIVersion() (major, minor uint32, err error) {
	if err := Load(); err != nil {
		return 0, 0, err
	}
	v := fns.ClientAPIVersion()
	return uint32(v >> 16), uint32(v & 0xffff), nil
}

func newFFILib(m []byte, dir string) *ffi.Library {
	lib := ffi.New("mpv", m)
	if dir != "" {
		lib.From(dir)
	}
	return lib
}

func defaultLibFileName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libmpv.dylib"
	case "windows":
		return "mpv-2.dll"
	default:
		return "libmpv.so"
	}
}

func findDevSystemLib(pkgDir string) string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	for _, c := range devSystemLibCandidates(runtime.GOOS, runtime.GOARCH, wd, exeDir, pkgDir) {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

func devSystemLibCandidates(goos, goarch, wd, exeDir, pkgDir string) []string {
	var candidates []string
	switch goos {
	case "darwin":
		candidates = []string{
			"/opt/homebrew/lib/libmpv.dylib",
			"/opt/homebrew/lib/libmpv.2.dylib",
			"/usr/local/lib/libmpv.dylib",
			"/usr/local/lib/libmpv.2.dylib",
		}
	case "linux":
		target := goos + "-" + goarch
		var localDirs []string
		if wd != "" {
			localDirs = append(localDirs, filepath.Join(wd, ".mygo", "native", target))
		}
		if pkgDir != "" && pkgDir != wd {
			localDirs = append(localDirs, filepath.Join(pkgDir, ".mygo", "native", target))
		}
		for _, d := range localDirs {
			candidates = append(candidates,
				filepath.Join(d, "libmpv.so"),
				filepath.Join(d, "libmpv.so.2"),
			)
		}
		var sysDirs []string
		switch goarch {
		case "amd64":
			sysDirs = append(sysDirs, "/usr/lib/x86_64-linux-gnu")
		case "arm64":
			sysDirs = append(sysDirs, "/usr/lib/aarch64-linux-gnu")
		}
		sysDirs = append(sysDirs, "/usr/lib64", "/usr/lib", "/usr/local/lib")
		for _, d := range sysDirs {
			candidates = append(candidates,
				d+"/libmpv.so.2",
				d+"/libmpv.so",
			)
		}
	case "windows":
		names := []string{"mpv-2.dll", "libmpv-2.dll", "mpv.dll"}
		target := goos + "-" + goarch
		var dirs []string
		if exeDir != "" {
			dirs = append(dirs, exeDir)
		}
		if wd != "" {
			dirs = append(dirs, wd, filepath.Join(wd, ".mygo", "native", target))
		}
		if pkgDir != "" && pkgDir != wd {
			dirs = append(dirs, filepath.Join(pkgDir, ".mygo", "native", target))
		}
		for _, d := range dirs {
			for _, name := range names {
				candidates = append(candidates, filepath.Join(d, name))
			}
		}
	}
	return candidates
}

func bindPathWithFFI(path string, target *libFuncs) error {
	if !platformSupported {
		return fmt.Errorf("mpv: %s/%s: %w", runtime.GOOS, runtime.GOARCH, errors.ErrUnsupported)
	}
	if path == "" {
		return errors.New("mpv: empty library path")
	}

	envBindMu.Lock()
	defer envBindMu.Unlock()

	envKey := ffi.EnvVar("mpv")
	prev, hadPrev := os.LookupEnv(envKey)
	if err := os.Setenv(envKey, path); err != nil {
		return fmt.Errorf("mpv: set %s: %w", envKey, err)
	}
	defer func() {
		if hadPrev {
			_ = os.Setenv(envKey, prev)
		} else {
			_ = os.Unsetenv(envKey)
		}
	}()

	if err := ffi.New("mpv", nil).Bind(target); err != nil {
		return fmt.Errorf("mpv: %w", err)
	}
	return nil
}

func checkErr(op string, code int32) error {
	if code >= 0 {
		return nil
	}
	return &Error{Code: ErrorCode(code), Op: op, Msg: ErrorString(ErrorCode(code))}
}
