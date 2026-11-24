//go:build !darwin && !linux

package mpv

import (
	"errors"
	"fmt"
	"runtime"
)

func dlopenLocal(path string) (uintptr, error) {
	return 0, fmt.Errorf("mpv: direct library load on %s (%s): %w", runtime.GOOS, path, errors.ErrUnsupported)
}

func dlsym(uintptr, string) (uintptr, error) {
	return 0, errors.ErrUnsupported
}
