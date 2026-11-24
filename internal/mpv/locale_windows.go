//go:build windows

package mpv

import (
	"sync"
	"syscall"
	"unsafe"
)

const lcNumeric = 4 // LC_NUMERIC in Windows UCRT <locale.h>

var (
	localeOnce sync.Once
	// localeMu serializes every setlocale call made by this package:
	// setlocale is not thread-safe and players may be created concurrently.
	localeMu      sync.Mutex
	setlocaleProc *syscall.Proc
	cLocaleBytes  = []byte("C\x00")
)

func loadSetlocale() {
	localeOnce.Do(func() {
		dll, err := syscall.LoadDLL("ucrtbase.dll")
		if err != nil {
			return
		}
		proc, err := dll.FindProc("setlocale")
		if err != nil {
			return
		}
		setlocaleProc = proc
	})
}

// ensureNumericLocale sets LC_NUMERIC to "C", which libmpv requires before
// mpv_create / mpv_initialize succeed.
func ensureNumericLocale() {
	loadSetlocale()
	if setlocaleProc == nil {
		return
	}
	localeMu.Lock()
	_, _, _ = setlocaleProc.Call(uintptr(lcNumeric), uintptr(unsafe.Pointer(&cLocaleBytes[0])))
	localeMu.Unlock()
}

// numericLocale returns the current LC_NUMERIC locale name ("" if unknown).
func numericLocale() string {
	loadSetlocale()
	if setlocaleProc == nil {
		return ""
	}
	localeMu.Lock()
	defer localeMu.Unlock()
	r, _, _ := setlocaleProc.Call(uintptr(lcNumeric), 0)
	if r == 0 {
		return ""
	}
	return GoString(*(*unsafe.Pointer)(unsafe.Pointer(&r)))
}
