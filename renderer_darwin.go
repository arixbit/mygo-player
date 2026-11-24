//go:build darwin

package player

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo/ffi"
	"github.com/egoist/mygo/native"
	"github.com/egoist/mygo/ui"

	"github.com/arixbit/mygo-player/internal/mpv"
)

// macOS CGL / OpenGL / IOSurface / CoreFoundation constants verified against MacOSX.sdk.
const (
	kCGLPFANoRecovery            int32 = 72
	kCGLPFAAccelerated           int32 = 73
	kCGLPFAAllowOfflineRenderers int32 = 96
	kCGLPFAOpenGLProfile         int32 = 99
	kCGLOGLPVersion32Core        int32 = 0x3200

	glRGBA                 uint32 = 0x1908
	glRGBA8                uint32 = 0x8058
	glBGRA                 uint32 = 0x80E1
	glUnsignedInt8888Rev   uint32 = 0x8367
	glLinear               int32  = 0x2601
	glClampToEdge          int32  = 0x812F
	glTextureMagFilter     uint32 = 0x2800
	glTextureMinFilter     uint32 = 0x2801
	glTextureWrapS         uint32 = 0x2802
	glTextureWrapT         uint32 = 0x2803
	glTextureRectangle     uint32 = 0x84F5
	glFramebuffer          uint32 = 0x8D40
	glColorAttachment0     uint32 = 0x8CE0
	glFramebufferComplete  uint32 = 0x8CD5
	kCFNumberSInt32Type    int64  = 3
	kCVPixelFormatTypeBGRA int32  = 0x42475241 // 'BGRA'

	// native.Texture retains pending + shown + 3 in-flight surfaces (= 5 max),
	// so 6 slots guarantees a free IOSurfaceIsInUse == 0 slot in steady state.
	surfacePoolCap = 6
)

func defaultHWDec() HWDecMode { return HWDecAutoSafe }

func defaultAudioOutput() string { return "" }

var apiTypeOpenGL = []byte("opengl\x00")

var darwinGL struct {
	once sync.Once
	err  error

	glHandle uintptr

	cglChoosePixelFormat  func(attribs *int32, pix *uintptr, npix *int32) int32
	cglDestroyPixelFormat func(pix uintptr) int32
	cglCreateContext      func(pix uintptr, share uintptr, ctx *uintptr) int32
	cglDestroyContext     func(ctx uintptr) int32
	cglSetCurrentContext  func(ctx uintptr) int32
	cglTexImageIOSurface  uintptr // 9-arg C call invoked via purego.SyscallN

	glGenFramebuffers        func(n int32, ids *uint32)
	glDeleteFramebuffers     func(n int32, ids *uint32)
	glBindFramebuffer        func(target uint32, fbo uint32)
	glFramebufferTexture2D   func(target, attachment, textarget uint32, tex uint32, level int32)
	glCheckFramebufferStatus func(target uint32) uint32
	glGenTextures            func(n int32, ids *uint32)
	glDeleteTextures         func(n int32, ids *uint32)
	glBindTexture            func(target uint32, tex uint32)
	glTexParameteri          func(target, pname uint32, param int32)
	glFlush                  func()
	glFinish                 func()

	cfNumberCreate     func(alloc uintptr, typ int64, val unsafe.Pointer) uintptr
	cfDictionaryCreate func(alloc uintptr, keys, vals unsafe.Pointer, num int64, keyCB, valCB uintptr) uintptr
	cfRetain           func(cf uintptr) uintptr
	cfRelease          func(cf uintptr)
	cfDictKeyCB        uintptr
	cfDictValCB        uintptr

	ioSurfaceCreate  func(props uintptr) uintptr
	ioSurfaceIsInUse func(surf uintptr) uint8
	keyWidth         uintptr
	keyHeight        uintptr
	keyBytesPerElem  uintptr
	keyPixelFormat   uintptr

	getProcAddrCB uintptr
}

func initDarwinGL() error {
	darwinGL.once.Do(func() {
		open := func(path string) (uintptr, error) {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				return 0, fmt.Errorf("dlopen %s: %w", path, err)
			}
			return h, nil
		}
		gl, err := open("/System/Library/Frameworks/OpenGL.framework/OpenGL")
		if err != nil {
			darwinGL.err = err
			return
		}
		cf, err := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		if err != nil {
			darwinGL.err = err
			return
		}
		io, err := open("/System/Library/Frameworks/IOSurface.framework/IOSurface")
		if err != nil {
			darwinGL.err = err
			return
		}
		darwinGL.glHandle = gl

		sym := func(lib uintptr, name string) (uintptr, error) {
			p, err := purego.Dlsym(lib, name)
			if err != nil {
				return 0, fmt.Errorf("dlsym %s: %w", name, err)
			}
			return p, nil
		}
		globalPtr := func(lib uintptr, name string) (uintptr, error) {
			p, err := sym(lib, name)
			if err != nil {
				return 0, err
			}
			return **(**uintptr)(unsafe.Pointer(&p)), nil
		}

		if darwinGL.cglTexImageIOSurface, err = sym(gl, "CGLTexImageIOSurface2D"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.cfDictKeyCB, err = sym(cf, "kCFTypeDictionaryKeyCallBacks"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.cfDictValCB, err = sym(cf, "kCFTypeDictionaryValueCallBacks"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.keyWidth, err = globalPtr(io, "kIOSurfaceWidth"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.keyHeight, err = globalPtr(io, "kIOSurfaceHeight"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.keyBytesPerElem, err = globalPtr(io, "kIOSurfaceBytesPerElement"); err != nil {
			darwinGL.err = err
			return
		}
		if darwinGL.keyPixelFormat, err = globalPtr(io, "kIOSurfacePixelFormat"); err != nil {
			darwinGL.err = err
			return
		}

		reg := func(fptr any, lib uintptr, name string) error {
			p, err := sym(lib, name)
			if err != nil {
				return err
			}
			purego.RegisterFunc(fptr, p)
			return nil
		}

		for _, item := range []struct {
			ptr  any
			lib  uintptr
			name string
		}{
			{&darwinGL.cglChoosePixelFormat, gl, "CGLChoosePixelFormat"},
			{&darwinGL.cglDestroyPixelFormat, gl, "CGLDestroyPixelFormat"},
			{&darwinGL.cglCreateContext, gl, "CGLCreateContext"},
			{&darwinGL.cglDestroyContext, gl, "CGLDestroyContext"},
			{&darwinGL.cglSetCurrentContext, gl, "CGLSetCurrentContext"},
			{&darwinGL.glGenFramebuffers, gl, "glGenFramebuffers"},
			{&darwinGL.glDeleteFramebuffers, gl, "glDeleteFramebuffers"},
			{&darwinGL.glBindFramebuffer, gl, "glBindFramebuffer"},
			{&darwinGL.glFramebufferTexture2D, gl, "glFramebufferTexture2D"},
			{&darwinGL.glCheckFramebufferStatus, gl, "glCheckFramebufferStatus"},
			{&darwinGL.glGenTextures, gl, "glGenTextures"},
			{&darwinGL.glDeleteTextures, gl, "glDeleteTextures"},
			{&darwinGL.glBindTexture, gl, "glBindTexture"},
			{&darwinGL.glTexParameteri, gl, "glTexParameteri"},
			{&darwinGL.glFlush, gl, "glFlush"},
			{&darwinGL.glFinish, gl, "glFinish"},
			{&darwinGL.cfNumberCreate, cf, "CFNumberCreate"},
			{&darwinGL.cfDictionaryCreate, cf, "CFDictionaryCreate"},
			{&darwinGL.cfRetain, cf, "CFRetain"},
			{&darwinGL.cfRelease, cf, "CFRelease"},
			{&darwinGL.ioSurfaceCreate, io, "IOSurfaceCreate"},
			{&darwinGL.ioSurfaceIsInUse, io, "IOSurfaceIsInUse"},
		} {
			if err := reg(item.ptr, item.lib, item.name); err != nil {
				darwinGL.err = err
				return
			}
		}

		// One permanent callback shared across all mpv render contexts.
		darwinGL.getProcAddrCB = purego.NewCallback(func(_ uintptr, namePtr uintptr) uintptr {
			if namePtr == 0 {
				return 0
			}
			name := ffi.GoString(*(*unsafe.Pointer)(unsafe.Pointer(&namePtr)))
			if name == "" {
				return 0
			}
			addr, err := purego.Dlsym(darwinGL.glHandle, name)
			if err != nil {
				return 0
			}
			return addr
		})
	})
	return darwinGL.err
}

type surfaceSlot struct {
	surface uintptr
	tex     uint32
	fbo     uint32
	w, h    int
}

type darwinRenderer struct {
	client *mpv.Client
	tex    *native.Texture

	wake chan struct{}
	stop chan struct{}
	done chan struct{}

	sizeMu       sync.Mutex
	explicitW    int
	explicitH    int
	videoW       int
	videoH       int
	forceRedraw  atomic.Bool
	framesDrawn  atomic.Uint64
	lastRenderMu sync.Mutex
	lastErr      error
}

func newRenderer(client *mpv.Client, _ func()) (*native.Texture, frameRenderer, error) {
	if err := initDarwinGL(); err != nil {
		return nil, nil, fmt.Errorf("player: macOS OpenGL/IOSurface init failed: %w", err)
	}
	tex := native.NewTexture()
	r := &darwinRenderer{
		client: client,
		tex:    tex,
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	ready := make(chan error, 1)
	go r.run(ready)
	if err := <-ready; err != nil {
		<-r.done
		return nil, nil, err
	}
	return tex, r, nil
}

func (r *darwinRenderer) SetViewportSize(w, h int) {
	w = clampRenderDim(w)
	h = clampRenderDim(h)
	r.sizeMu.Lock()
	changed := r.explicitW != w || r.explicitH != h
	r.explicitW, r.explicitH = w, h
	r.sizeMu.Unlock()
	if changed {
		r.forceRedraw.Store(true)
		r.signalWake()
	}
}

func (r *darwinRenderer) SetVideoSize(w, h int) {
	w = clampRenderDim(w)
	h = clampRenderDim(h)
	r.sizeMu.Lock()
	changed := r.videoW != w || r.videoH != h
	r.videoW, r.videoH = w, h
	r.sizeMu.Unlock()
	if changed {
		r.forceRedraw.Store(true)
		r.signalWake()
	}
}

func (r *darwinRenderer) RequestRedraw() {
	r.forceRedraw.Store(true)
	r.signalWake()
}

func (r *darwinRenderer) FramesRendered() uint64 {
	return r.framesDrawn.Load()
}

func (r *darwinRenderer) FrameSize() (int, int) {
	if r.tex == nil {
		return 0, 0
	}
	w, h := r.tex.Size()
	return int(w), int(h)
}

func (r *darwinRenderer) ImageSource() ui.ImageSource {
	return r.tex
}

func (r *darwinRenderer) Close() {
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
	r.signalWake()
	<-r.done
}

func (r *darwinRenderer) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *darwinRenderer) targetSize() (int, int) {
	r.sizeMu.Lock()
	ew, eh := r.explicitW, r.explicitH
	vw, vh := r.videoW, r.videoH
	r.sizeMu.Unlock()
	if ew > 0 && eh > 0 {
		return ew, eh
	}
	if vw > 0 && vh > 0 {
		return vw, vh
	}
	return 0, 0
}

func (r *darwinRenderer) run(ready chan<- error) {
	defer close(r.done)

	// CGLContextObj is bound to the OS thread for the entire renderer lifetime.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cglCtx, err := createCGLContext()
	if err != nil {
		ready <- err
		return
	}
	defer func() {
		darwinGL.cglSetCurrentContext(0)
		darwinGL.cglDestroyContext(cglCtx)
	}()

	if code := darwinGL.cglSetCurrentContext(cglCtx); code != 0 {
		ready <- fmt.Errorf("player: CGLSetCurrentContext failed (%d)", code)
		return
	}

	initParams := mpv.OpenGLInitParams{
		GetProcAddress: darwinGL.getProcAddrCB,
	}
	adv := int32(1)
	createParams := []mpv.RenderParam{
		{Type: mpv.RenderParamAPIType, Data: unsafe.Pointer(&apiTypeOpenGL[0])},
		{Type: mpv.RenderParamOpenGLInitParams, Data: unsafe.Pointer(&initParams)},
		{Type: mpv.RenderParamAdvancedControl, Data: unsafe.Pointer(&adv)},
		{Type: mpv.RenderParamInvalid},
	}
	rctx, err := mpv.NewRenderContext(r.client, createParams)
	runtime.KeepAlive(&initParams)
	runtime.KeepAlive(&adv)
	if err != nil {
		ready <- fmt.Errorf("player: create mpv OpenGL render context: %w", err)
		return
	}

	var pool []*surfaceSlot
	var poolW, poolH int
	var rrIdx int
	hasFrame := false
	pendingFrame := false

	destroyPool := func() {
		for _, s := range pool {
			destroySlot(s)
		}
		pool = nil
		poolW, poolH = 0, 0
		rrIdx = 0
	}
	defer func() {
		// Free mpv render context BEFORE destroying GL pool & CGL context.
		rctx.Free()
		destroyPool()
	}()

	rctx.SetUpdateCallback(func() {
		r.signalWake()
	})
	ready <- nil

	acquireSlot := func(w, h int) (*surfaceSlot, error) {
		if poolW != w || poolH != h {
			destroyPool()
			poolW, poolH = w, h
		}
		for _, s := range pool {
			if darwinGL.ioSurfaceIsInUse(s.surface) == 0 {
				return s, nil
			}
		}
		if len(pool) < surfacePoolCap {
			s, err := createSlot(cglCtx, w, h)
			if err != nil {
				return nil, err
			}
			pool = append(pool, s)
			return s, nil
		}
		// Fallback if all slots are unexpectedly still in use.
		s := pool[rrIdx%len(pool)]
		rrIdx++
		return s, nil
	}

	for {
		select {
		case <-r.stop:
			return
		case <-r.wake:
		}
		select {
		case <-r.stop:
			return
		default:
		}

		flags := rctx.Update()
		forced := r.forceRedraw.Swap(false)
		needRender := (flags & mpv.RenderUpdateFrame) != 0
		if needRender {
			pendingFrame = true
		}
		if !pendingFrame && !(forced && hasFrame) {
			continue
		}

		w, h := r.targetSize()
		if w <= 0 || h <= 0 {
			continue
		}

		slot, err := acquireSlot(w, h)
		if err != nil {
			r.lastRenderMu.Lock()
			r.lastErr = err
			r.lastRenderMu.Unlock()
			continue
		}

		fbo := mpv.OpenGLFBO{
			FBO:            int32(slot.fbo),
			W:              int32(w),
			H:              int32(h),
			InternalFormat: int32(glRGBA8),
		}
		// Keep FLIP_Y = 0: OpenGL FBO memory row 0 (NDC y = -1) maps directly
		// to IOSurface/Metal row 0 (top of image), so libmpv's default FBO
		// orientation is upright when sampled by MyGo's Metal renderer.
		flipY := int32(0)
		renderParams := []mpv.RenderParam{
			{Type: mpv.RenderParamOpenGLFBO, Data: unsafe.Pointer(&fbo)},
			{Type: mpv.RenderParamFlipY, Data: unsafe.Pointer(&flipY)},
			{Type: mpv.RenderParamInvalid},
		}
		err = rctx.Render(renderParams)
		runtime.KeepAlive(&fbo)
		runtime.KeepAlive(&flipY)
		if err != nil {
			r.lastRenderMu.Lock()
			r.lastErr = err
			r.lastRenderMu.Unlock()
			continue
		}

		// Finish OpenGL rendering so the IOSurface backing memory is fully
		// synchronized before Metal or CPU (IOSurfaceLock) reads it.
		darwinGL.glFinish()

		// Present takes ownership of one CF reference to the IOSurfaceRef.
		darwinGL.cfRetain(slot.surface)
		r.tex.Present(slot.surface)
		rctx.ReportSwap()
		pendingFrame = false
		hasFrame = true
		r.framesDrawn.Add(1)
	}
}

func createCGLContext() (uintptr, error) {
	tryAttribs := [][]int32{
		{
			kCGLPFAOpenGLProfile, kCGLOGLPVersion32Core,
			kCGLPFAAccelerated,
			kCGLPFANoRecovery,
			kCGLPFAAllowOfflineRenderers,
			0,
		},
		{
			kCGLPFAOpenGLProfile, kCGLOGLPVersion32Core,
			kCGLPFAAllowOfflineRenderers,
			0,
		},
	}

	var pix uintptr
	var npix int32
	var lastCode int32
	for _, attribs := range tryAttribs {
		lastCode = darwinGL.cglChoosePixelFormat(&attribs[0], &pix, &npix)
		runtime.KeepAlive(attribs)
		if lastCode == 0 && pix != 0 {
			break
		}
	}
	if pix == 0 {
		return 0, fmt.Errorf("player: CGLChoosePixelFormat failed (%d)", lastCode)
	}
	defer darwinGL.cglDestroyPixelFormat(pix)

	var ctx uintptr
	if code := darwinGL.cglCreateContext(pix, 0, &ctx); code != 0 || ctx == 0 {
		return 0, fmt.Errorf("player: CGLCreateContext failed (%d)", code)
	}
	return ctx, nil
}

func createIOSurfaceBGRA(w, h int) (uintptr, error) {
	makeNum := func(v int32) uintptr {
		return darwinGL.cfNumberCreate(0, kCFNumberSInt32Type, unsafe.Pointer(&v))
	}
	nW := makeNum(int32(w))
	nH := makeNum(int32(h))
	nBPE := makeNum(4)
	nFmt := makeNum(kCVPixelFormatTypeBGRA)
	defer func() {
		darwinGL.cfRelease(nW)
		darwinGL.cfRelease(nH)
		darwinGL.cfRelease(nBPE)
		darwinGL.cfRelease(nFmt)
	}()

	keys := [4]uintptr{
		darwinGL.keyWidth,
		darwinGL.keyHeight,
		darwinGL.keyBytesPerElem,
		darwinGL.keyPixelFormat,
	}
	vals := [4]uintptr{nW, nH, nBPE, nFmt}
	dict := darwinGL.cfDictionaryCreate(
		0,
		unsafe.Pointer(&keys[0]),
		unsafe.Pointer(&vals[0]),
		int64(len(keys)),
		darwinGL.cfDictKeyCB,
		darwinGL.cfDictValCB,
	)
	runtime.KeepAlive(&keys)
	runtime.KeepAlive(&vals)
	if dict == 0 {
		return 0, errors.New("player: CFDictionaryCreate returned NULL")
	}
	defer darwinGL.cfRelease(dict)

	surf := darwinGL.ioSurfaceCreate(dict)
	if surf == 0 {
		return 0, fmt.Errorf("player: IOSurfaceCreate(%dx%d) failed", w, h)
	}
	return surf, nil
}

func createSlot(cglCtx uintptr, w, h int) (*surfaceSlot, error) {
	surf, err := createIOSurfaceBGRA(w, h)
	if err != nil {
		return nil, err
	}

	var tex uint32
	darwinGL.glGenTextures(1, &tex)
	darwinGL.glBindTexture(glTextureRectangle, tex)
	darwinGL.glTexParameteri(glTextureRectangle, glTextureMinFilter, glLinear)
	darwinGL.glTexParameteri(glTextureRectangle, glTextureMagFilter, glLinear)
	darwinGL.glTexParameteri(glTextureRectangle, glTextureWrapS, glClampToEdge)
	darwinGL.glTexParameteri(glTextureRectangle, glTextureWrapT, glClampToEdge)

	code, _, _ := purego.SyscallN(
		darwinGL.cglTexImageIOSurface,
		cglCtx,
		uintptr(glTextureRectangle),
		uintptr(glRGBA),
		uintptr(w),
		uintptr(h),
		uintptr(glBGRA),
		uintptr(glUnsignedInt8888Rev),
		surf,
		0,
	)
	if int32(code) != 0 {
		darwinGL.glBindTexture(glTextureRectangle, 0)
		darwinGL.glDeleteTextures(1, &tex)
		darwinGL.cfRelease(surf)
		return nil, fmt.Errorf("player: CGLTexImageIOSurface2D(%dx%d) failed (%d)", w, h, int32(code))
	}

	var fbo uint32
	darwinGL.glGenFramebuffers(1, &fbo)
	darwinGL.glBindFramebuffer(glFramebuffer, fbo)
	darwinGL.glFramebufferTexture2D(glFramebuffer, glColorAttachment0, glTextureRectangle, tex, 0)
	status := darwinGL.glCheckFramebufferStatus(glFramebuffer)
	darwinGL.glBindFramebuffer(glFramebuffer, 0)
	darwinGL.glBindTexture(glTextureRectangle, 0)
	if status != glFramebufferComplete {
		darwinGL.glDeleteFramebuffers(1, &fbo)
		darwinGL.glDeleteTextures(1, &tex)
		darwinGL.cfRelease(surf)
		return nil, fmt.Errorf("player: FBO incomplete (0x%x) for %dx%d", status, w, h)
	}

	return &surfaceSlot{
		surface: surf,
		tex:     tex,
		fbo:     fbo,
		w:       w,
		h:       h,
	}, nil
}

func destroySlot(s *surfaceSlot) {
	if s == nil {
		return
	}
	if s.fbo != 0 {
		fbo := s.fbo
		darwinGL.glDeleteFramebuffers(1, &fbo)
		s.fbo = 0
	}
	if s.tex != 0 {
		tex := s.tex
		darwinGL.glDeleteTextures(1, &tex)
		s.tex = 0
	}
	if s.surface != 0 {
		darwinGL.cfRelease(s.surface)
		s.surface = 0
	}
}
