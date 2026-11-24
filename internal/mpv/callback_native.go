//go:build (darwin && !ios) || (linux && !android) || windows

package mpv

import "github.com/ebitengine/purego"

const platformSupported = true

func newRenderUpdateCallback(fn func(uintptr) uintptr) uintptr {
	return purego.NewCallback(fn)
}
