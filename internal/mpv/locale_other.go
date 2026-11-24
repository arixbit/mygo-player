//go:build (!darwin || ios) && (!linux || android) && !windows

package mpv

func ensureNumericLocale() {}

// numericLocale reports "C": unsupported platforms never load libmpv, so the
// numeric locale requirement is trivially satisfied.
func numericLocale() string { return "C" }
