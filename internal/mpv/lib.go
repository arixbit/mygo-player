package mpv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ffi"
)

// libFuncs holds the dynamically bound C functions of libmpv.
type libFuncs struct {
	ClientAPIVersion func() uint64                                                              `ffi:"mpv_client_api_version"`
	ErrorString      func(err int32) string                                                     `ffi:"mpv_error_string"`
	Free             func(data unsafe.Pointer)                                                  `ffi:"mpv_free"`
	Create           func() uintptr                                                             `ffi:"mpv_create"`
	Initialize       func(ctx uintptr) int32                                                    `ffi:"mpv_initialize"`
	TerminateDestroy func(ctx uintptr)                                                          `ffi:"mpv_terminate_destroy"`
	SetOption        func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32    `ffi:"mpv_set_option"`
	SetOptionString  func(ctx uintptr, name string, data string) int32                          `ffi:"mpv_set_option_string"`
	Command          func(ctx uintptr, args **byte) int32                                       `ffi:"mpv_command"`
	CommandAsync     func(ctx uintptr, replyUserdata uint64, args **byte) int32                 `ffi:"mpv_command_async"`
	CommandString    func(ctx uintptr, args string) int32                                       `ffi:"mpv_command_string"`
	CommandNode      func(ctx uintptr, args *Node, result *Node) int32                          `ffi:"mpv_command_node"`
	SetProperty      func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32    `ffi:"mpv_set_property"`
	SetPropertyStr   func(ctx uintptr, name string, data string) int32                          `ffi:"mpv_set_property_string"`
	SetPropertyAsync func(ctx uintptr, replyUserdata uint64, name string, format int32, data unsafe.Pointer) int32 `ffi:"mpv_set_property_async"`
	GetProperty      func(ctx uintptr, name string, format int32, data unsafe.Pointer) int32    `ffi:"mpv_get_property"`
	GetPropertyStr   func(ctx uintptr, name string) unsafe.Pointer                              `ffi:"mpv_get_property_string"`
	FreeNodeContents func(node *Node)                                                           `ffi:"mpv_free_node_contents"`
	ObserveProperty  func(ctx uintptr, replyUserdata uint64, name string, format int32) int32   `ffi:"mpv_observe_property"`
	UnobserveProp    func(ctx uintptr, registeredReplyUserdata uint64) int32                    `ffi:"mpv_unobserve_property"`
	RequestLogMsgs   func(ctx uintptr, minLevel string) int32                                   `ffi:"mpv_request_log_messages"`
	WaitEvent        func(ctx uintptr, timeout float64) unsafe.Pointer                          `ffi:"mpv_wait_event"`
	Wakeup           func(ctx uintptr)                                                          `ffi:"mpv_wakeup"`

	RenderContextCreate            func(res *uintptr, ctx uintptr, params *RenderParam) int32   `ffi:"mpv_render_context_create"`
	RenderContextSetUpdateCallback func(rctx uintptr, callback uintptr, callbackCtx uintptr)    `ffi:"mpv_render_context_set_update_callback"`
	RenderContextUpdate            func(rctx uintptr) uint64                                    `ffi:"mpv_render_context_update"`
	RenderContextRender            func(rctx uintptr, params *RenderParam) int32                `ffi:"mpv_render_context_render"`
	RenderContextReportSwap        func(rctx uintptr)                                           `ffi:"mpv_render_context_report_swap"`
	RenderContextFree              func(rctx uintptr)                                           `ffi:"mpv_render_context_free"`
}

var (
	loaderMu   sync.Mutex
	manifest   []byte
	packageDir string
	customPath string

	loadedOnce sync.Once
	loadedPath string
	loadedErr  error
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
// standard local macOS installation paths in unpackaged development runs.
func LibraryPath() (string, error) {
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
		if sysPath := findDevSystemLib(); sysPath != "" {
			return sysPath, nil
		}
	}
	return "", fmt.Errorf("mpv: %w (set %s=/path/to/libmpv.dylib or install libmpv)", err, ffi.EnvVar("mpv"))
}

// Load loads libmpv and binds its C symbols once.
func Load() error {
	loadedOnce.Do(func() {
		loaderMu.Lock()
		p := customPath
		m := manifest
		dir := packageDir
		loaderMu.Unlock()

		if p != "" {
			abs, err := filepath.Abs(p)
			if err != nil {
				loadedErr = err
				return
			}
			loadedPath = abs
			loadedErr = loadDirect(abs)
			return
		}

		lib := newFFILib(m, dir)
		if path, err := lib.Path(); err == nil {
			if err := lib.Bind(&fns); err != nil {
				loadedErr = err
				return
			}
			loadedPath = path
			return
		} else {
			if !mygo.App.IsPackaged() && os.Getenv(ffi.EnvVar("mpv")) == "" {
				if sysPath := findDevSystemLib(); sysPath != "" {
					loadedPath = sysPath
					loadedErr = loadDirect(sysPath)
					return
				}
			}
			loadedErr = fmt.Errorf("mpv: %w (set %s=/path/to/libmpv.dylib or install libmpv)", err, ffi.EnvVar("mpv"))
		}
	})
	return loadedErr
}

// LoadedPath returns the path of the loaded libmpv library, if Load succeeded.
func LoadedPath() string {
	return loadedPath
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

func findDevSystemLib() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	candidates := []string{
		"/opt/homebrew/lib/libmpv.dylib",
		"/opt/homebrew/lib/libmpv.2.dylib",
		"/usr/local/lib/libmpv.dylib",
		"/usr/local/lib/libmpv.2.dylib",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

func loadDirect(path string) error {
	handle, err := dlopenLocal(path)
	if err != nil {
		return fmt.Errorf("mpv: dlopen %s: %w", path, err)
	}
	bindings := []struct {
		ptr  any
		name string
	}{
		{&fns.ClientAPIVersion, "mpv_client_api_version"},
		{&fns.ErrorString, "mpv_error_string"},
		{&fns.Free, "mpv_free"},
		{&fns.Create, "mpv_create"},
		{&fns.Initialize, "mpv_initialize"},
		{&fns.TerminateDestroy, "mpv_terminate_destroy"},
		{&fns.SetOption, "mpv_set_option"},
		{&fns.SetOptionString, "mpv_set_option_string"},
		{&fns.Command, "mpv_command"},
		{&fns.CommandAsync, "mpv_command_async"},
		{&fns.CommandString, "mpv_command_string"},
		{&fns.CommandNode, "mpv_command_node"},
		{&fns.SetProperty, "mpv_set_property"},
		{&fns.SetPropertyStr, "mpv_set_property_string"},
		{&fns.SetPropertyAsync, "mpv_set_property_async"},
		{&fns.GetProperty, "mpv_get_property"},
		{&fns.GetPropertyStr, "mpv_get_property_string"},
		{&fns.FreeNodeContents, "mpv_free_node_contents"},
		{&fns.ObserveProperty, "mpv_observe_property"},
		{&fns.UnobserveProp, "mpv_unobserve_property"},
		{&fns.RequestLogMsgs, "mpv_request_log_messages"},
		{&fns.WaitEvent, "mpv_wait_event"},
		{&fns.Wakeup, "mpv_wakeup"},
		{&fns.RenderContextCreate, "mpv_render_context_create"},
		{&fns.RenderContextSetUpdateCallback, "mpv_render_context_set_update_callback"},
		{&fns.RenderContextUpdate, "mpv_render_context_update"},
		{&fns.RenderContextRender, "mpv_render_context_render"},
		{&fns.RenderContextReportSwap, "mpv_render_context_report_swap"},
		{&fns.RenderContextFree, "mpv_render_context_free"},
	}
	for _, b := range bindings {
		sym, err := dlsym(handle, b.name)
		if err != nil {
			return fmt.Errorf("mpv: %s missing symbol %s: %w", filepath.Base(path), b.name, err)
		}
		if err := registerFuncSafe(b.ptr, sym, b.name); err != nil {
			return err
		}
	}
	return nil
}

func registerFuncSafe(fptr any, addr uintptr, name string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mpv: binding %s: %v", name, r)
		}
	}()
	purego.RegisterFunc(fptr, addr)
	return nil
}

func checkErr(op string, code int32) error {
	if code >= 0 {
		return nil
	}
	msg := ""
	if fns.ErrorString != nil {
		msg = fns.ErrorString(code)
	}
	return &Error{Code: ErrorCode(code), Op: op, Msg: msg}
}
