//go:build windows

package player

import (
	"github.com/egoist/mygo/native"

	"github.com/arixbit/mygo-player/internal/mpv"
)

func defaultHWDec() HWDecMode { return HWDecAutoCopy }

func defaultAudioOutput() string { return "wasapi" }

func newRenderer(client *mpv.Client, onFrame func()) (*native.Texture, frameRenderer, error) {
	r, err := newSWRenderer(client, onFrame)
	if err != nil {
		return nil, nil, err
	}
	return nil, r, nil
}
