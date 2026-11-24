//go:build (!darwin || ios) && (!linux || android) && !windows

package mpv

const platformSupported = false

func newRenderUpdateCallback(func(uintptr) uintptr) uintptr {
	return 0
}
