package mpv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

var configureOnce sync.Once

// requireLibmpv loads the real libmpv or fails (MYGO_REQUIRE_LIBMPV=1) / skips.
// Like the root package's init, it configures mygo/ffi with the repository's
// mygo-plugin.json so the published libmpv build is discovered.
func requireLibmpv(t *testing.T) {
	t.Helper()
	configureOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			return
		}
		if m, err := os.ReadFile(filepath.Join(root, "mygo-plugin.json")); err == nil {
			Configure(m, root)
		}
	})
	if err := Load(); err != nil {
		if os.Getenv("MYGO_REQUIRE_LIBMPV") == "1" {
			t.Fatalf("libmpv required (MYGO_REQUIRE_LIBMPV=1) but failed to load: %v", err)
		}
		t.Skipf("libmpv not available: %v", err)
	}
}

// resetLoaderForTest forgets the cached successful load so Load retries.
func resetLoaderForTest() {
	loadMu.Lock()
	loaded = false
	loadedPath = ""
	loadMu.Unlock()
}

// newHeadlessClient creates and initializes a real libmpv client with no
// audio/video output.
func newHeadlessClient(t *testing.T) *Client {
	t.Helper()
	c, err := Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, kv := range [][2]string{{"vo", "null"}, {"ao", "null"}, {"idle", "yes"}, {"config", "no"}} {
		if err := c.SetOptionString(kv[0], kv[1]); err != nil {
			c.TerminateDestroy()
			t.Fatalf("SetOptionString(%s): %v", kv[0], err)
		}
	}
	return c
}

func TestSafetyLoadRetryAfterFailure(t *testing.T) {
	requireLibmpv(t)
	good := LoadedPath()
	if good == "" {
		t.Fatal("LoadedPath() empty after successful Load")
	}
	t.Cleanup(func() { SetLibraryPath("") })

	resetLoaderForTest()
	SetLibraryPath(filepath.Join(t.TempDir(), "missing", "libmpv-nonexistent.dylib"))
	if err := Load(); err == nil {
		t.Fatal("Load succeeded with a nonexistent library path")
	}
	if got := LoadedPath(); got != "" {
		t.Fatalf("LoadedPath() after failed Load = %q, want empty", got)
	}
	// The failure must not be cached: fixing the path makes Load succeed.
	SetLibraryPath(good)
	if err := Load(); err != nil {
		t.Fatalf("Load retry with valid path failed: %v", err)
	}
	want, _ := filepath.Abs(good)
	if got := LoadedPath(); got != want {
		t.Fatalf("LoadedPath() = %q, want %q", got, want)
	}
	// Symbols are usable after the retry.
	if major, _, err := ClientAPIVersion(); err != nil || major < 2 {
		t.Fatalf("ClientAPIVersion after retry = %d, %v", major, err)
	}
}

func TestSafetyLoadedPathConcurrent(t *testing.T) {
	requireLibmpv(t)
	want := LoadedPath()
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for range 32 {
		wg.Go(func() {
			if err := Load(); err != nil {
				errs <- "Load: " + err.Error()
				return
			}
			if got := LoadedPath(); got != want {
				errs <- "LoadedPath=" + got
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestSafetyErrorString(t *testing.T) {
	// Built-in table works even without consulting libmpv.
	if got := errorTexts[ErrLoadingFailed]; got != "loading failed" {
		t.Fatalf("errorTexts[-13] = %q", got)
	}
	if got := ErrorString(ErrorCode(-999)); !strings.Contains(got, "-999") {
		t.Fatalf("ErrorString(-999) = %q, want mention of code", got)
	}
	requireLibmpv(t)
	for code, text := range errorTexts {
		got := ErrorString(code)
		if got != text {
			t.Errorf("ErrorString(%d) = %q, libmpv/table mismatch with %q", code, got, text)
		}
		if lib := fns.ErrorString(int32(code)); lib != text {
			t.Errorf("mpv_error_string(%d) = %q, table has %q", code, lib, text)
		}
	}
	err := checkErr("loadfile", int32(ErrLoadingFailed))
	if err == nil || err.Error() != "mpv loadfile: loading failed (-13)" {
		t.Fatalf("checkErr(-13) = %v", err)
	}
	if checkErr("ok", 0) != nil {
		t.Fatal("checkErr(0) != nil")
	}
}

// clientCalls exercises every Client method and returns their errors.
func clientCalls(c *Client) map[string]error {
	out := map[string]error{}
	out["Initialize"] = c.Initialize()
	out["SetOptionString"] = c.SetOptionString("pause", "yes")
	out["SetOptionFlag"] = c.SetOptionFlag("pause", true)
	out["Command"] = c.Command("set", "pause", "no")
	out["CommandAsync"] = c.CommandAsync(1, "set", "pause", "no")
	out["CommandString"] = c.CommandString("set pause no")
	out["SetPropertyString"] = c.SetPropertyString("pause", "no")
	out["SetPropertyFlag"] = c.SetPropertyFlag("pause", true)
	out["SetPropertyInt64"] = c.SetPropertyInt64("osd-level", 1)
	out["SetPropertyFloat64"] = c.SetPropertyFloat64("speed", 1.25)
	out["SetPropertyStringList"] = c.SetPropertyStringList("http-header-fields", []string{"A: b"})
	_, out["GetPropertyString"] = c.GetPropertyString("pause")
	_, out["GetPropertyFlag"] = c.GetPropertyFlag("pause")
	_, out["GetPropertyInt64"] = c.GetPropertyInt64("osd-level")
	_, out["GetPropertyFloat64"] = c.GetPropertyFloat64("speed")
	_, out["GetPropertyNode"] = c.GetPropertyNode("http-header-fields")
	out["ObserveProperty"] = c.ObserveProperty(9, "pause", FormatFlag)
	out["UnobserveProperty"] = c.UnobserveProperty(9)
	out["RequestLogMessages"] = c.RequestLogMessages("warn")
	_, out["NewRenderContext"] = NewRenderContext(c, []RenderParam{{Type: RenderParamInvalid}})
	return out
}

func TestSafetyNilClientReturnsErrors(t *testing.T) {
	for name, c := range map[string]*Client{"nil": nil, "zero": {}} {
		for call, err := range clientCalls(c) {
			if err == nil {
				t.Errorf("%s client: %s returned nil error", name, call)
				continue
			}
			if !errors.Is(err, ErrNilClient) {
				t.Errorf("%s client: %s error %v is not ErrNilClient", name, call, err)
			}
			var mErr *Error
			if !errors.As(err, &mErr) || mErr.Code != ErrUninitialized {
				t.Errorf("%s client: %s error %v lacks ErrUninitialized code", name, call, err)
			}
		}
		if ev := c.WaitEvent(0); ev != nil {
			t.Errorf("%s client: WaitEvent = %v, want nil", name, ev)
		}
		if h := c.Handle(); h != 0 {
			t.Errorf("%s client: Handle = %#x", name, h)
		}
		c.Wakeup()           // must not panic
		c.TerminateDestroy() // must not panic
	}
}

func TestSafetyRealClientLifecycleAndUseAfterDestroy(t *testing.T) {
	requireLibmpv(t)
	c := newHeadlessClient(t)
	if err := c.SetOptionFlag("pause", true); err != nil {
		t.Fatalf("SetOptionFlag before init: %v", err)
	}
	if err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := c.RequestLogMessages("warn"); err != nil {
		t.Fatalf("RequestLogMessages: %v", err)
	}
	if v, err := c.GetPropertyFlag("pause"); err != nil || !v {
		t.Fatalf("GetPropertyFlag(pause) = %v, %v; want true", v, err)
	}
	if err := c.SetPropertyString("pause", "no"); err != nil {
		t.Fatalf("SetPropertyString: %v", err)
	}
	if s, err := c.GetPropertyString("pause"); err != nil || s != "no" {
		t.Fatalf("GetPropertyString(pause) = %q, %v", s, err)
	}
	if err := c.SetPropertyFlag("pause", true); err != nil {
		t.Fatalf("SetPropertyFlag: %v", err)
	}
	if err := c.SetPropertyInt64("osd-level", 2); err != nil {
		t.Fatalf("SetPropertyInt64: %v", err)
	}
	if v, err := c.GetPropertyInt64("osd-level"); err != nil || v != 2 {
		t.Fatalf("GetPropertyInt64(osd-level) = %d, %v", v, err)
	}
	if err := c.SetPropertyFloat64("speed", 1.5); err != nil {
		t.Fatalf("SetPropertyFloat64: %v", err)
	}
	if v, err := c.GetPropertyFloat64("speed"); err != nil || v != 1.5 {
		t.Fatalf("GetPropertyFloat64(speed) = %v, %v", v, err)
	}
	if err := c.SetPropertyStringList("http-header-fields", []string{"X-A: 1,2", "X-B: \"q\""}); err != nil {
		t.Fatalf("SetPropertyStringList: %v", err)
	}
	node, err := c.GetPropertyNode("http-header-fields")
	if err != nil {
		t.Fatalf("GetPropertyNode: %v", err)
	}
	if list, ok := node.([]any); !ok || len(list) != 2 || list[0] != "X-A: 1,2" || list[1] != "X-B: \"q\"" {
		t.Fatalf("GetPropertyNode(http-header-fields) = %#v", node)
	}
	if devs, err := c.GetPropertyNode("audio-device-list"); err != nil {
		t.Fatalf("GetPropertyNode(audio-device-list): %v", err)
	} else if list, ok := devs.([]any); !ok || len(list) == 0 {
		t.Fatalf("audio-device-list = %#v, want non-empty list", devs)
	} else if _, ok := list[0].(map[string]any); !ok {
		t.Fatalf("audio-device-list[0] = %#v, want map", list[0])
	}
	if _, err := c.GetPropertyString("no-such-property-xyz"); err == nil {
		t.Fatal("GetPropertyString(unknown) succeeded")
	}
	if err := c.CommandString("set pause no"); err != nil {
		t.Fatalf("CommandString: %v", err)
	}
	if err := c.Command("set", "pause", "yes"); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if err := c.Command("no-such-command-xyz"); err == nil {
		t.Fatal("Command(unknown) succeeded")
	}
	if err := c.ObserveProperty(42, "speed", FormatDouble); err != nil {
		t.Fatalf("ObserveProperty: %v", err)
	}
	if err := c.CommandAsync(77, "set", "speed", "2"); err != nil {
		t.Fatalf("CommandAsync: %v", err)
	}
	gotReply, gotProp := false, false
	deadline := time.Now().Add(3 * time.Second)
	for (!gotReply || !gotProp) && time.Now().Before(deadline) {
		ev := c.WaitEvent(0.2)
		if ev == nil {
			continue
		}
		switch ev.ID {
		case EventCommandReply:
			gotReply = gotReply || ev.ReplyUserdata == 77
		case EventPropertyChange:
			if ev.ReplyUserdata == 42 && ev.Data != nil {
				ep := (*EventProperty)(ev.Data)
				if ep.Format == FormatDouble && *(*float64)(ep.Data) == 2 {
					gotProp = true
				}
			}
		}
	}
	if !gotReply || !gotProp {
		t.Fatalf("async command reply=%v property change=%v", gotReply, gotProp)
	}
	if err := c.UnobserveProperty(42); err != nil {
		t.Fatalf("UnobserveProperty: %v", err)
	}
	c.Wakeup()

	// After TerminateDestroy every call must fail cleanly instead of passing a
	// freed handle to libmpv.
	c.TerminateDestroy()
	c.TerminateDestroy() // idempotent
	for call, err := range clientCalls(c) {
		if !errors.Is(err, ErrNilClient) {
			t.Errorf("after destroy: %s = %v, want ErrNilClient", call, err)
		}
	}
	if ev := c.WaitEvent(0); ev != nil {
		t.Errorf("after destroy: WaitEvent = %v, want nil", ev)
	}
}

func TestSafetyLocaleConcurrent(t *testing.T) {
	requireLibmpv(t)
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(ensureNumericLocale)
	}
	// Concurrent client creation also calls setlocale under the same lock.
	for range 6 {
		wg.Go(func() {
			c, err := Create()
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			c.TerminateDestroy()
		})
	}
	wg.Wait()
	if got := numericLocale(); got != "C" {
		t.Fatalf("LC_NUMERIC = %q after concurrent ensureNumericLocale, want C", got)
	}
}

func TestSafetyDecodeNodeShapes(t *testing.T) {
	cstr := []byte("hi\x00")
	if got := decodeNode(Node{U: uint64(uintptr(unsafe.Pointer(&cstr[0]))), Format: FormatString}); got != "hi" {
		t.Fatalf("string node = %#v", got)
	}
	if got := decodeNode(Node{Format: FormatNone}); got != nil {
		t.Fatalf("none node = %#v", got)
	}
	if got := decodeNode(Node{Format: FormatNodeArray}); got != nil && len(got.([]any)) != 0 {
		t.Fatalf("nil array node = %#v", got)
	}
	if got := decodeNode(Node{Format: FormatNodeMap}); got != nil && len(got.(map[string]any)) != 0 {
		t.Fatalf("nil map node = %#v", got)
	}
	if got := decodeNode(Node{Format: FormatByteArray}); got != nil && len(got.([]byte)) != 0 {
		t.Fatalf("nil bytearray node = %#v", got)
	}
	payload := []byte{1, 2, 3}
	ba := ByteArray{Data: unsafe.Pointer(&payload[0]), Size: uintptr(len(payload))}
	got := decodeNode(Node{U: uint64(uintptr(unsafe.Pointer(&ba))), Format: FormatByteArray})
	if b, ok := got.([]byte); !ok || len(b) != 3 || b[2] != 3 {
		t.Fatalf("bytearray node = %#v", got)
	}
	if got := decodeNode(Node{Format: Format(99)}); got != nil {
		t.Fatalf("unknown node = %#v", got)
	}
}
