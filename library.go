package player

import (
	_ "embed"
	"path/filepath"
	"runtime"

	"github.com/arixbit/mygo-player/internal/mpv"
)

//go:embed mygo-plugin.json
var pluginManifest []byte

func init() {
	var dir string
	if _, file, _, ok := runtime.Caller(0); ok {
		dir = filepath.Dir(file)
	}
	mpv.Configure(pluginManifest, dir)
}

// SetLibraryPath overrides the libmpv shared library path before Load or New is
// called. Most applications should rely on mygo-plugin.json or MYGO_NATIVE_MPV
// instead.
func SetLibraryPath(path string) {
	mpv.SetLibraryPath(path)
}

// LibraryPath returns the resolved path of the libmpv dynamic library.
func LibraryPath() (string, error) {
	return mpv.LibraryPath()
}

// Load loads libmpv and binds all symbols once. New calls Load automatically;
// call Load earlier to verify libmpv availability at startup.
func Load() error {
	return mpv.Load()
}

// ClientAPIVersion returns the loaded libmpv client API major and minor version.
func ClientAPIVersion() (major, minor uint32, err error) {
	return mpv.ClientAPIVersion()
}
