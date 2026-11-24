//go:build !darwin

package player

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/egoist/mygo/native"

	"github.com/arixbit/mygo-player/internal/mpv"
)

func newRenderer(_ *mpv.Client, _ *native.Texture) (frameRenderer, error) {
	return nil, fmt.Errorf("player: zero-copy renderer not implemented on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}
