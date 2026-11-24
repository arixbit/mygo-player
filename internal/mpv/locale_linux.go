//go:build linux && !android

package mpv

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const lcNumeric = 1 // LC_NUMERIC in Linux glibc/musl <locale.h>

var (
	localeOnce sync.Once
	// localeMu serializes every setlocale call made by this package:
	// setlocale is not thread-safe and players may be created concurrently.
	localeMu       sync.Mutex
	setlocale      func(category int32, locale string) uintptr
	setlocaleQuery func(category int32, locale unsafe.Pointer) unsafe.Pointer
)

func loadSetlocale() {
	localeOnce.Do(func() {
		lib, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			lib = purego.RTLD_DEFAULT
		}
		if _, err := purego.Dlsym(lib, "setlocale"); err != nil {
			return
		}
		purego.RegisterLibFunc(&setlocale, lib, "setlocale")
		purego.RegisterLibFunc(&setlocaleQuery, lib, "setlocale")
	})
}

// ensureNumericLocale sets LC_NUMERIC to "C", which libmpv requires before
// mpv_create / mpv_initialize succeed.
func ensureNumericLocale() {
	loadSetlocale()
	if setlocale == nil {
		return
	}
	localeMu.Lock()
	setlocale(lcNumeric, "C")
	localeMu.Unlock()
}

// numericLocale returns the current LC_NUMERIC locale name ("" if unknown).
func numericLocale() string {
	loadSetlocale()
	if setlocaleQuery == nil {
		return ""
	}
	localeMu.Lock()
	defer localeMu.Unlock()
	p := setlocaleQuery(lcNumeric, nil)
	if p == nil {
		return ""
	}
	return GoString(p)
}
