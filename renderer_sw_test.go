package player

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/arixbit/mygo-player/internal/mpv"
)

func fillNV12Split(buf *swFrameBuf, w, h int, topY, topCb, topCr, botY, botCb, botCr byte) {
	buf.ensureNV12(w, h)
	yPlane := buf.yuv.Planes[0]
	uvPlane := buf.yuv.Planes[1]
	stride := buf.yuv.Strides[0]
	for y := range h {
		yv := topY
		if y >= h/2 {
			yv = botY
		}
		row := yPlane[y*stride : y*stride+w]
		for x := range w {
			row[x] = yv
		}
	}
	ch := (h + 1) / 2
	cw := (w + 1) / 2
	for y := range ch {
		cb, cr := topCb, topCr
		if y >= ch/2 {
			cb, cr = botCb, botCr
		}
		row := uvPlane[y*stride : y*stride+2*cw]
		for x := range cw {
			row[2*x] = cb
			row[2*x+1] = cr
		}
	}
}

func TestSWRendererDoubleBufferNV12UpdateYUV(t *testing.T) {
	r := &swRenderer{displayedIdx: -1, readyIdx: -1}

	// BT.601 limited-range YCbCr approximations:
	//   Pure Red  (255,0,0) -> Y=81, Cb=90,  Cr=240
	//   Pure Blue (0,0,255) -> Y=41, Cb=240, Cr=110
	idx0 := r.acquireWriteIndex()
	if idx0 != 0 {
		t.Fatalf("first acquireWriteIndex = %d, want 0", idx0)
	}
	fillNV12Split(&r.bufs[idx0], 64, 64, 81, 90, 240, 41, 240, 110)
	r.publishWriteIndex(idx0, 64, 64)

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Image(c, r.ImageSource()).Fill().Fit(ui.FillBox)
	}, 64, 64)
	tt.Frame()

	topC := tt.Image().RGBAAt(32, 16)
	botC := tt.Image().RGBAAt(32, 48)
	if topC.R < 200 || topC.G > 40 || topC.B > 40 {
		t.Fatalf("NV12 frame 1 top expected RED, got %+v", topC)
	}
	if botC.B < 200 || botC.R > 40 || botC.G > 40 {
		t.Fatalf("NV12 frame 1 bottom expected BLUE, got %+v", botC)
	}

	// Next write must use the alternate double-buffer slot so displayed slot 0
	// stays untouched while the UI holds it.
	idx1 := r.acquireWriteIndex()
	if idx1 != 1 {
		t.Fatalf("second acquireWriteIndex = %d, want 1", idx1)
	}
	fillNV12Split(&r.bufs[idx1], 64, 64, 41, 240, 110, 81, 90, 240)
	r.publishWriteIndex(idx1, 64, 64)

	tt.Frame()
	topC2 := tt.Image().RGBAAt(32, 16)
	botC2 := tt.Image().RGBAAt(32, 48)
	if topC2.B < 200 || topC2.R > 40 || topC2.G > 40 {
		t.Fatalf("NV12 frame 2 top expected BLUE, got %+v", topC2)
	}
	if botC2.R < 200 || botC2.G > 40 || botC2.B > 40 {
		t.Fatalf("NV12 frame 2 bottom expected RED, got %+v", botC2)
	}
}

func TestSWRendererDoubleBufferRGBA(t *testing.T) {
	r := &swRenderer{displayedIdx: -1, readyIdx: -1}

	fillSolid := func(w, h int, cr, cg, cb byte) {
		idx := r.acquireWriteIndex()
		rgba, stride := r.bufs[idx].ensureRGBA(w, h)
		if int(stride) != 4*w || rgba.Stride != 4*w {
			t.Fatalf("stride=%d rgba.Stride=%d want %d", stride, rgba.Stride, 4*w)
		}
		if uintptr(unsafe.Pointer(&rgba.Pix[0]))%swBufAlign != 0 {
			t.Fatalf("RGBA buffer not %d-byte aligned", swBufAlign)
		}
		for i := 0; i < len(rgba.Pix); i += 4 {
			rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = cr, cg, cb, 255
		}
		r.publishWriteIndex(idx, w, h)
	}

	fillSolid(48, 32, 255, 0, 0)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Image(c, r.ImageSource()).Fill().Fit(ui.FillBox)
	}, 64, 48)
	tt.Frame()
	if got := tt.Image().RGBAAt(32, 24); got.R < 220 || got.B > 20 {
		t.Fatalf("RGBA frame 1 expected RED, got %+v", got)
	}

	// Resize + color flip in second slot.
	fillSolid(64, 48, 0, 0, 255)
	if w, h := r.FrameSize(); w != 64 || h != 48 {
		t.Fatalf("FrameSize = %dx%d, want 64x48", w, h)
	}
	tt.Frame()
	if got := tt.Image().RGBAAt(32, 24); got.B < 220 || got.R > 20 {
		t.Fatalf("RGBA frame 2 expected BLUE, got %+v", got)
	}
}

// TestIntegrationSWRendererRealLibmpv verifies the exact Windows SW rendering
// path (MPV_RENDER_API_TYPE_SW + NV12 probe / RGBA fallback + double-buffered
// ui.Bitmap + singleton update callback) against a real libmpv library on both
// macOS and Windows.
func TestIntegrationSWRendererRealLibmpv(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	tmp := t.TempDir()
	mkvPath, _ := generateTestMKV(t, ffmpeg, tmp)

	for cycle := range 2 {
		client, err := mpv.Create()
		if err != nil {
			t.Fatalf("cycle %d: mpv.Create: %v", cycle, err)
		}
		for _, kv := range [][2]string{
			{"vo", "libmpv"},
			{"vid", "auto"},
			{"idle", "yes"},
			{"config", "no"},
			{"osd-level", "0"},
			{"hwdec", string(HWDecAutoCopy)},
			{"ao", "null"},
			{"keep-open", "yes"},
		} {
			if err := client.SetOptionString(kv[0], kv[1]); err != nil {
				client.TerminateDestroy()
				t.Fatalf("cycle %d: set option %s=%s: %v", cycle, kv[0], kv[1], err)
			}
		}
		if err := client.Initialize(); err != nil {
			client.TerminateDestroy()
			t.Fatalf("cycle %d: mpv.Initialize: %v", cycle, err)
		}

		var frameNotifies atomic.Uint64
		r, err := newSWRenderer(client, func() {
			frameNotifies.Add(1)
		})
		if err != nil {
			client.TerminateDestroy()
			t.Fatalf("cycle %d: newSWRenderer: %v", cycle, err)
		}

		r.SetViewportSize(320, 240)
		if err := client.Command("loadfile", mkvPath, "replace"); err != nil {
			r.Close()
			client.TerminateDestroy()
			t.Fatalf("cycle %d: loadfile: %v", cycle, err)
		}

		// Wait until playback has actually loaded and decoded frames (not just
		// an initial idle clear frame).
		waitFor(t, 8*time.Second, "SW renderer decoded video frame", func() bool {
			w, h := r.FrameSize()
			dur, err := client.GetPropertyFloat64("duration")
			return err == nil && dur > 0 && r.FramesRendered() > 2 && frameNotifies.Load() > 2 && w == 320 && h == 240
		})
		t.Logf("cycle %d: swRenderer activeFormat=%s frames=%d",
			cycle, swFormatMode(r.activeFormat.Load()), r.FramesRendered())

		// Inspect raw SW buffer bytes before UI compositing to verify R,G,B,A directly.
		r.frameMu.Lock()
		inspectIdx := r.readyIdx
		if inspectIdx < 0 {
			inspectIdx = r.displayedIdx
		}
		if inspectIdx >= 0 && r.bufs[inspectIdx].rgba != nil {
			rawTop := r.bufs[inspectIdx].rgba.RGBAAt(160, 50)
			rawBot := r.bufs[inspectIdx].rgba.RGBAAt(160, 190)
			t.Logf("cycle %d: raw SW RGBA top=%+v bot=%+v", cycle, rawTop, rawBot)
		}
		r.frameMu.Unlock()

		tt := ui.NewTester(func(c *ui.Context) {
			ui.Image(c, r.ImageSource()).Fill().Fit(ui.Contain).Background(ui.RGB(0, 0, 0))
		}, 320, 240)
		tt.Frame()
		img := tt.Image()
		topC := img.RGBAAt(160, 50)
		botC := img.RGBAAt(160, 190)
		if topC.R < 180 || topC.G > 60 || topC.B > 60 {
			r.Close()
			client.TerminateDestroy()
			t.Fatalf("cycle %d: SW renderer top expected RED, got %+v", cycle, topC)
		}
		if botC.B < 180 || botC.R > 60 || botC.G > 60 {
			r.Close()
			client.TerminateDestroy()
			t.Fatalf("cycle %d: SW renderer bottom expected BLUE, got %+v", cycle, botC)
		}

		// Pause and verify viewport resize re-renders cleanly into the new size.
		if err := client.SetPropertyFlag("pause", true); err != nil {
			r.Close()
			client.TerminateDestroy()
			t.Fatalf("cycle %d: pause: %v", cycle, err)
		}
		r.SetViewportSize(640, 480)
		waitFor(t, 4*time.Second, "SW renderer resize 640x480", func() bool {
			w, h := r.FrameSize()
			return w == 640 && h == 480
		})

		r.Close()
		client.TerminateDestroy()
	}
}
