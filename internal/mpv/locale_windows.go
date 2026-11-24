//go:build windows

package mpv

import (
	"sync"
	"syscall"
	"unsafe"
)

const lcNumeric = 4 // LC_NUMERIC in Windows UCRT <locale.h>

var (
	localeOnce    sync.Once
	setlocaleProc *syscall.Proc
	cLocaleBytes  = []byte("C\x00")
)

// ensureNumericLocale sets LC_NUMERIC to "C", which libmpv requires before
// mpv_create / mpv_initialize succeed.
func ensureNumericLocale() {
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
	if setlocaleProc != nil {
		_, _, _ = setlocaleProc.Call(uintptr(lcNumeric), uintptr(unsafe.Pointer(&cLocaleBytes[0])))
	}
}
