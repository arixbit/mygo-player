//go:build !darwin && !windows

package player

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/egoist/mygo/native"

	"github.com/arixbit/mygo-player/internal/mpv"
)

func defaultHWDec() HWDecMode { return HWDecAutoSafe }

func defaultAudioOutput() string { return "" }

func newRenderer(_ *mpv.Client, _ func()) (*native.Texture, frameRenderer, error) {
	return nil, nil, fmt.Errorf("player: renderer not implemented on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}
