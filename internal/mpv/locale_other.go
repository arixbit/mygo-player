//go:build (!darwin || ios) && (!linux || android) && !windows

package mpv

func ensureNumericLocale() {}
