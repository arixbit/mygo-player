package player

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/arixbit/mygo-player/internal/mpv"
)

const (
	maxRenderDim = 7680
	swBufAlign   = 64
)

var (
	apiTypeSW = []byte("sw\x00")
	swFmtNV12 = []byte("nv12\x00")
	// Use "rgb0" instead of "rgba": both share the exact [R,G,B,X] 4-byte pixel
	// layout, but "rgba" has MP_IMGFLAG_ALPHA which causes mpv's sub/draw_bmp.c
	// to require zimg for premul/unpremul when blending subtitles/OSD (failing
	// with "Failed rendering OSD." on LGPL builds without zimg). "rgb0" takes
	// mpv's fast 8-bit direct OSD/subtitle blending path, and we stamp alpha=255
	// afterwards for MyGo's image.RGBA.
	swFmtRGB0 = []byte("rgb0\x00")
)

type swFormatMode int32

const (
	swFormatAuto swFormatMode = iota
	swFormatNV12
	swFormatRGBA
)

func (m swFormatMode) String() string {
	switch m {
	case swFormatNV12:
		return "nv12"
	case swFormatRGBA:
		return "rgba"
	default:
		return "auto"
	}
}

// swFrameBuf holds one slot of the double-buffered software render target.
// Backing memory is 64-byte aligned for SIMD scaling/conversion inside libmpv
// and reused across frames of matching dimensions.
type swFrameBuf struct {
	mode swFormatMode
	w, h int

	raw  []byte
	rgba *image.RGBA
	yuv  ui.YUVImage
}

func (b *swFrameBuf) ensureAlignedBytes(need int) []byte {
	if need <= 0 {
		return nil
	}
	if cap(b.raw) < need+swBufAlign {
		b.raw = make([]byte, need+swBufAlign)
	} else {
		b.raw = b.raw[:need+swBufAlign]
	}
	base := uintptr(unsafe.Pointer(&b.raw[0]))
	off := int((swBufAlign - (base & (swBufAlign - 1))) & (swBufAlign - 1))
	return b.raw[off : off+need]
}

// ensureRGBA prepares a tightly packed 4*w stride RGBA buffer so
// ui.Bitmap.Update takes Pix without copying.
func (b *swFrameBuf) ensureRGBA(w, h int) (*image.RGBA, uintptr) {
	stride := w * 4
	need := stride * h
	if b.mode == swFormatRGBA && b.rgba != nil && b.w == w && b.h == h && len(b.rgba.Pix) == need {
		return b.rgba, uintptr(stride)
	}
	pix := b.ensureAlignedBytes(need)
	b.mode = swFormatRGBA
	b.w, b.h = w, h
	b.yuv = ui.YUVImage{}
	b.rgba = &image.RGBA{
		Pix:    pix,
		Stride: stride,
		Rect:   image.Rect(0, 0, w, h),
	}
	return b.rgba, uintptr(stride)
}

// ensureNV12 prepares a contiguous 64-byte-stride NV12 buffer (luma followed
// by interleaved CbCr chroma) for zero-copy ui.Bitmap.UpdateYUV presentation.
func (b *swFrameBuf) ensureNV12(w, h int) (unsafe.Pointer, uintptr) {
	stride := alignUp(w, swBufAlign)
	ySize := stride * h
	uvSize := stride * ((h + 1) / 2)
	need := ySize + uvSize
	matrix := defaultYUVMatrix(w, h)
	if b.mode == swFormatNV12 && b.w == w && b.h == h &&
		len(b.yuv.Planes[0]) == ySize && len(b.yuv.Planes[1]) == uvSize &&
		b.yuv.Strides[0] == stride {
		b.yuv.Matrix = matrix
		return unsafe.Pointer(&b.yuv.Planes[0][0]), uintptr(stride)
	}
	pix := b.ensureAlignedBytes(need)
	b.mode = swFormatNV12
	b.w, b.h = w, h
	b.rgba = nil
	b.yuv = ui.YUVImage{
		Format: ui.NV12,
		Width:  w,
		Height: h,
		Planes: [3][]byte{
			pix[:ySize],
			pix[ySize : ySize+uvSize],
			nil,
		},
		Strides: [3]int{stride, stride, 0},
		Matrix:  matrix,
	}
	return unsafe.Pointer(&pix[0]), uintptr(stride)
}

// swRenderer renders libmpv frames via MPV_RENDER_API_TYPE_SW into a
// double-buffered backing store and presents them into a MyGo ui.Bitmap
// (preferring UpdateYUV NV12, falling back to Update RGBA).
type swRenderer struct {
	client  *mpv.Client
	onFrame func()

	wake chan struct{}
	stop chan struct{}
	done chan struct{}

	sizeMu      sync.Mutex
	explicitW   int
	explicitH   int
	videoW      int
	videoH      int
	forceRedraw atomic.Bool
	framesDrawn atomic.Uint64

	// Wait-free triple-buffer mailbox handoff between the render goroutine and
	// the UI thread. The worker always renders into a slot distinct from both
	// displayedIdx (held by UI/GPU) and readyIdx (latest completed frame), so a
	// completed frame is never cleared while the next frame is rendering.
	frameMu      sync.Mutex
	bufs         [3]swFrameBuf
	displayedIdx int
	readyIdx     int
	lastW        int
	lastH        int
	bitmap       *ui.Bitmap

	activeFormat atomic.Int32

	lastErrMu sync.Mutex
	lastErr   error
}

func newSWRenderer(client *mpv.Client, onFrame func()) (*swRenderer, error) {
	r := &swRenderer{
		client:       client,
		onFrame:      onFrame,
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		displayedIdx: -1,
		readyIdx:     -1,
	}
	ready := make(chan error, 1)
	go r.run(ready)
	if err := <-ready; err != nil {
		<-r.done
		return nil, err
	}
	return r, nil
}

func (r *swRenderer) SetViewportSize(w, h int) {
	w, h = normalizeSWDim(w, h)
	r.sizeMu.Lock()
	changed := r.explicitW != w || r.explicitH != h
	r.explicitW, r.explicitH = w, h
	r.sizeMu.Unlock()
	if changed {
		r.forceRedraw.Store(true)
		r.signalWake()
	}
}

func (r *swRenderer) SetVideoSize(w, h int) {
	w, h = normalizeSWDim(w, h)
	r.sizeMu.Lock()
	changed := r.videoW != w || r.videoH != h
	r.videoW, r.videoH = w, h
	r.sizeMu.Unlock()
	if changed {
		r.forceRedraw.Store(true)
		r.signalWake()
	}
}

func (r *swRenderer) RequestRedraw() {
	r.forceRedraw.Store(true)
	r.signalWake()
}

func (r *swRenderer) FramesRendered() uint64 {
	return r.framesDrawn.Load()
}

func (r *swRenderer) FrameSize() (int, int) {
	r.frameMu.Lock()
	w, h := r.lastW, r.lastH
	r.frameMu.Unlock()
	return w, h
}

// ImageSource synchronizes the latest double-buffered frame into the UI-thread
// ui.Bitmap (via UpdateYUV for NV12 or Update for RGBA) and returns it.
func (r *swRenderer) ImageSource() ui.ImageSource {
	r.frameMu.Lock()
	if r.readyIdx >= 0 {
		idx := r.readyIdx
		r.readyIdx = -1
		r.displayedIdx = idx
		r.applyFrameToBitmapLocked(&r.bufs[idx])
	}
	bm := r.bitmap
	r.frameMu.Unlock()
	if bm == nil {
		return nil
	}
	return bm
}

func (r *swRenderer) applyFrameToBitmapLocked(f *swFrameBuf) {
	if f == nil || f.w <= 0 || f.h <= 0 {
		return
	}
	switch f.mode {
	case swFormatNV12:
		if r.bitmap == nil {
			r.bitmap = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		yuv := f.yuv
		r.bitmap.UpdateYUV(&yuv)
	case swFormatRGBA:
		if f.rgba == nil {
			return
		}
		if r.bitmap == nil {
			r.bitmap = ui.NewBitmap(f.rgba)
		} else {
			r.bitmap.Update(f.rgba)
		}
	}
}

func (r *swRenderer) Close() {
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
	r.signalWake()
	<-r.done
}

func (r *swRenderer) signalWake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *swRenderer) targetSize() (int, int) {
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

// acquireWriteIndex picks a buffer slot distinct from both displayedIdx (held
// by the UI thread) and readyIdx (latest completed frame awaiting presentation).
func (r *swRenderer) acquireWriteIndex() int {
	r.frameMu.Lock()
	idx := 0
	for i := range len(r.bufs) {
		if i != r.displayedIdx && i != r.readyIdx {
			idx = i
			break
		}
	}
	r.frameMu.Unlock()
	return idx
}

func (r *swRenderer) publishWriteIndex(idx, w, h int) {
	r.frameMu.Lock()
	r.readyIdx = idx
	r.lastW, r.lastH = w, h
	r.frameMu.Unlock()
}

func (r *swRenderer) run(ready chan<- error) {
	defer close(r.done)

	adv := int32(1)
	createParams := []mpv.RenderParam{
		{Type: mpv.RenderParamAPIType, Data: unsafe.Pointer(&apiTypeSW[0])},
		{Type: mpv.RenderParamAdvancedControl, Data: unsafe.Pointer(&adv)},
		{Type: mpv.RenderParamInvalid},
	}
	rctx, err := mpv.NewRenderContext(r.client, createParams)
	runtime.KeepAlive(&adv)
	if err != nil {
		ready <- fmt.Errorf("player: create mpv SW render context: %w", err)
		return
	}
	defer rctx.Free()

	pixelFmt := probeSWFormat(rctx)
	r.activeFormat.Store(int32(pixelFmt))

	// Uses internal/mpv's singleton purego.NewCallback trampoline routed by ID.
	rctx.SetUpdateCallback(func() {
		r.signalWake()
	})
	ready <- nil

	hasFrame := false
	pendingFrame := false

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

		writeIdx := r.acquireWriteIndex()
		buf := &r.bufs[writeIdx]
		blockTarget := !forced || needRender

		var renderErr error
		if pixelFmt == swFormatNV12 {
			renderErr = renderSWNV12(rctx, buf, w, h, blockTarget)
			if renderErr != nil {
				// Libmpv rejected NV12 during actual frame conversion; permanently
				// fall back to RGBA and immediately retry this frame.
				pixelFmt = swFormatRGBA
				r.activeFormat.Store(int32(pixelFmt))
				renderErr = renderSWRGBA(rctx, buf, w, h, false)
			}
		} else {
			renderErr = renderSWRGBA(rctx, buf, w, h, blockTarget)
		}

		if renderErr != nil {
			r.lastErrMu.Lock()
			r.lastErr = renderErr
			r.lastErrMu.Unlock()
			continue
		}

		r.publishWriteIndex(writeIdx, w, h)
		rctx.ReportSwap()
		pendingFrame = false
		hasFrame = true
		r.framesDrawn.Add(1)
		if r.onFrame != nil {
			r.onFrame()
		}
	}
}

// probeSWFormat checks whether the loaded libmpv accepts NV12 for
// MPV_RENDER_API_TYPE_SW (preferred for ui.Bitmap.UpdateYUV) and otherwise
// selects RGBA (for ui.Bitmap.Update).
func probeSWFormat(rctx *mpv.RenderContext) swFormatMode {
	var probe swFrameBuf
	if err := renderSWNV12(rctx, &probe, 4, 4, false); err == nil {
		return swFormatNV12
	}
	return swFormatRGBA
}

func renderSWNV12(rctx *mpv.RenderContext, buf *swFrameBuf, w, h int, blockForTargetTime bool) error {
	ptr, stride := buf.ensureNV12(w, h)
	size := [2]int32{int32(w), int32(h)}
	var block int32
	if blockForTargetTime {
		block = 1
	}
	params := []mpv.RenderParam{
		{Type: mpv.RenderParamSWSize, Data: unsafe.Pointer(&size[0])},
		{Type: mpv.RenderParamSWFormat, Data: unsafe.Pointer(&swFmtNV12[0])},
		{Type: mpv.RenderParamSWStride, Data: unsafe.Pointer(&stride)},
		{Type: mpv.RenderParamSWPointer, Data: ptr},
		{Type: mpv.RenderParamBlockForTargetTime, Data: unsafe.Pointer(&block)},
		{Type: mpv.RenderParamInvalid},
	}
	err := rctx.Render(params)
	runtime.KeepAlive(&size)
	runtime.KeepAlive(&stride)
	runtime.KeepAlive(&block)
	runtime.KeepAlive(buf.raw)
	return err
}

func renderSWRGBA(rctx *mpv.RenderContext, buf *swFrameBuf, w, h int, blockForTargetTime bool) error {
	rgba, stride := buf.ensureRGBA(w, h)
	size := [2]int32{int32(w), int32(h)}
	var block int32
	if blockForTargetTime {
		block = 1
	}
	params := []mpv.RenderParam{
		{Type: mpv.RenderParamSWSize, Data: unsafe.Pointer(&size[0])},
		{Type: mpv.RenderParamSWFormat, Data: unsafe.Pointer(&swFmtRGB0[0])},
		{Type: mpv.RenderParamSWStride, Data: unsafe.Pointer(&stride)},
		{Type: mpv.RenderParamSWPointer, Data: unsafe.Pointer(&rgba.Pix[0])},
		{Type: mpv.RenderParamBlockForTargetTime, Data: unsafe.Pointer(&block)},
		{Type: mpv.RenderParamInvalid},
	}
	err := rctx.Render(params)
	runtime.KeepAlive(&size)
	runtime.KeepAlive(&stride)
	runtime.KeepAlive(&block)
	runtime.KeepAlive(buf.raw)
	if err == nil {
		fillOpaqueAlpha(rgba.Pix)
	}
	return err
}

func fillOpaqueAlpha(pix []byte) {
	for i := 3; i < len(pix); i += 4 {
		pix[i] = 0xff
	}
}

func clampRenderDim(v int) int {
	if v <= 0 {
		return 0
	}
	if v > maxRenderDim {
		return maxRenderDim
	}
	return v
}

func normalizeSWDim(w, h int) (int, int) {
	w = clampRenderDim(w)
	h = clampRenderDim(h)
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	w &^= 1
	h &^= 1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return w, h
}

func alignUp(v, align int) int {
	return (v + align - 1) &^ (align - 1)
}

func defaultYUVMatrix(w, h int) ui.YUVMatrix {
	if w >= 1280 || h >= 720 {
		return ui.BT709
	}
	return ui.BT601
}
