//go:build linux && !android

package mpv

import (
	"sync"

	"github.com/ebitengine/purego"
)

const lcNumeric = 1 // LC_NUMERIC in Linux glibc/musl <locale.h>

var (
	localeOnce sync.Once
	setlocale  func(category int32, locale string) uintptr
)

// ensureNumericLocale sets LC_NUMERIC to "C", which libmpv requires before
// mpv_create / mpv_initialize succeed.
func ensureNumericLocale() {
	localeOnce.Do(func() {
		lib, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			lib = purego.RTLD_DEFAULT
		}
		if _, err := purego.Dlsym(lib, "setlocale"); err != nil {
			return
		}
		purego.RegisterLibFunc(&setlocale, lib, "setlocale")
	})
	if setlocale != nil {
		setlocale(lcNumeric, "C")
	}
}
