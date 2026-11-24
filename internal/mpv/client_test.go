package mpv

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ffi"
)

func TestABIStructLayouts(t *testing.T) {
	if got := unsafe.Sizeof(Node{}); got != 16 {
		t.Fatalf("sizeof(Node) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(Node{}.Format); got != 8 {
		t.Fatalf("offsetof(Node.Format) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(NodeList{}); got != 24 {
		t.Fatalf("sizeof(NodeList) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(NodeList{}.Values); got != 8 {
		t.Fatalf("offsetof(NodeList.Values) = %d, want 8", got)
	}
	if got := unsafe.Offsetof(NodeList{}.Keys); got != 16 {
		t.Fatalf("offsetof(NodeList.Keys) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(Event{}); got != 24 {
		t.Fatalf("sizeof(Event) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(Event{}.Data); got != 16 {
		t.Fatalf("offsetof(Event.Data) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(EventProperty{}); got != 24 {
		t.Fatalf("sizeof(EventProperty) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(EventProperty{}.Data); got != 16 {
		t.Fatalf("offsetof(EventProperty.Data) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(RenderParam{}); got != 16 {
		t.Fatalf("sizeof(RenderParam) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(RenderParam{}.Data); got != 8 {
		t.Fatalf("offsetof(RenderParam.Data) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(OpenGLInitParams{}); got != 16 {
		t.Fatalf("sizeof(OpenGLInitParams) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(OpenGLFBO{}); got != 16 {
		t.Fatalf("sizeof(OpenGLFBO) = %d, want 16", got)
	}
}

func TestRenderUpdateTrampolineSingleton(t *testing.T) {
	if !platformSupported {
		if got := renderUpdateTrampoline(); got != 0 {
			t.Fatalf("renderUpdateTrampoline() on unsupported platform = %#x, want 0", got)
		}
		return
	}
	addr1 := renderUpdateTrampoline()
	addr2 := renderUpdateTrampoline()
	if addr1 == 0 {
		t.Fatal("renderUpdateTrampoline() returned 0")
	}
	if addr1 != addr2 {
		t.Fatalf("renderUpdateTrampoline() allocated distinct callbacks: %#x vs %#x", addr1, addr2)
	}
}

func TestDevSystemLibCandidates(t *testing.T) {
	wd := "/work"
	pkgDir := "/pkg"

	linuxAMD64 := devSystemLibCandidates("linux", "amd64", wd, "/bin", pkgDir)
	for _, want := range []string{
		filepath.Join(wd, ".mygo", "native", "linux-amd64", "libmpv.so"),
		filepath.Join(pkgDir, ".mygo", "native", "linux-amd64", "libmpv.so"),
		"/usr/lib/x86_64-linux-gnu/libmpv.so.2",
		"/usr/lib/x86_64-linux-gnu/libmpv.so",
		"/usr/lib64/libmpv.so.2",
		"/usr/lib64/libmpv.so",
		"/usr/lib/libmpv.so.2",
		"/usr/lib/libmpv.so",
		"/usr/local/lib/libmpv.so.2",
		"/usr/local/lib/libmpv.so",
	} {
		if !slices.Contains(linuxAMD64, want) {
			t.Errorf("linux/amd64 candidates missing %q: %v", want, linuxAMD64)
		}
	}
	if slices.Contains(linuxAMD64, "/usr/lib/aarch64-linux-gnu/libmpv.so.2") {
		t.Errorf("linux/amd64 candidates should not include aarch64 path: %v", linuxAMD64)
	}

	linuxARM64 := devSystemLibCandidates("linux", "arm64", wd, "/bin", pkgDir)
	for _, want := range []string{
		filepath.Join(wd, ".mygo", "native", "linux-arm64", "libmpv.so"),
		"/usr/lib/aarch64-linux-gnu/libmpv.so.2",
		"/usr/lib/aarch64-linux-gnu/libmpv.so",
	} {
		if !slices.Contains(linuxARM64, want) {
			t.Errorf("linux/arm64 candidates missing %q: %v", want, linuxARM64)
		}
	}
	if slices.Contains(linuxARM64, "/usr/lib/x86_64-linux-gnu/libmpv.so.2") {
		t.Errorf("linux/arm64 candidates should not include x86_64 path: %v", linuxARM64)
	}

	if got := devSystemLibCandidates("js", "wasm", wd, "/bin", pkgDir); len(got) != 0 {
		t.Errorf("js/wasm candidates = %v, want empty", got)
	}
}

func TestBindPathWithFFIRestoresEnvOnError(t *testing.T) {
	envKey := ffi.EnvVar("mpv")
	missingPath := filepath.Join(t.TempDir(), "nonexistent-libmpv.so")

	t.Run("preserves existing env", func(t *testing.T) {
		t.Setenv(envKey, "sentinel-path")
		var target libFuncs
		err := bindPathWithFFI(missingPath, &target)
		if err == nil {
			t.Fatal("bindPathWithFFI succeeded for nonexistent path")
		}
		if !platformSupported && !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("expected errors.ErrUnsupported on unsupported platform, got: %v", err)
		}
		if got, ok := os.LookupEnv(envKey); !ok || got != "sentinel-path" {
			t.Fatalf("%s = (%q, %v), want (%q, true)", envKey, got, ok, "sentinel-path")
		}
	})

	t.Run("restores unset env", func(t *testing.T) {
		prev, hadPrev := os.LookupEnv(envKey)
		if err := os.Unsetenv(envKey); err != nil {
			t.Fatalf("unset %s: %v", envKey, err)
		}
		t.Cleanup(func() {
			if hadPrev {
				_ = os.Setenv(envKey, prev)
			} else {
				_ = os.Unsetenv(envKey)
			}
		})

		var target libFuncs
		if err := bindPathWithFFI(missingPath, &target); err == nil {
			t.Fatal("bindPathWithFFI succeeded for nonexistent path")
		}
		if got, ok := os.LookupEnv(envKey); ok {
			t.Fatalf("%s = %q after bindPathWithFFI, want unset", envKey, got)
		}
	})

	t.Run("rejects empty path", func(t *testing.T) {
		t.Setenv(envKey, "keep-me")
		var target libFuncs
		if err := bindPathWithFFI("", &target); err == nil {
			t.Fatal("bindPathWithFFI succeeded for empty path")
		}
		if got := os.Getenv(envKey); got != "keep-me" {
			t.Fatalf("%s = %q, want %q", envKey, got, "keep-me")
		}
	})
}
