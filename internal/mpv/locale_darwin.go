//go:build darwin

package mpv

import (
	"sync"

	"github.com/ebitengine/purego"
)

const lcNumeric = 4 // LC_NUMERIC from macOS SDK <locale.h>

var (
	localeOnce sync.Once
	setlocale  func(category int32, locale string) uintptr
)

// ensureNumericLocale sets LC_NUMERIC to "C", which libmpv requires before
// mpv_initialize succeeds.
func ensureNumericLocale() {
	localeOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&setlocale, lib, "setlocale")
	})
	if setlocale != nil {
		setlocale(lcNumeric, "C")
	}
}
