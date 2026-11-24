package player

import (
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

type stubRenderer struct {
	mu            sync.Mutex
	src           ui.ImageSource
	viewportW     int
	viewportH     int
	viewportCalls int
	videoW        int
	videoH        int
	redrawCalls   int
	frames        uint64
	closed        bool
}

func (s *stubRenderer) SetViewportSize(w, h int) {
	s.mu.Lock()
	s.viewportW = w
	s.viewportH = h
	s.viewportCalls++
	s.mu.Unlock()
}

func (s *stubRenderer) SetVideoSize(w, h int) {
	s.mu.Lock()
	s.videoW = w
	s.videoH = h
	s.mu.Unlock()
}

func (s *stubRenderer) RequestRedraw() {
	s.mu.Lock()
	s.redrawCalls++
	s.mu.Unlock()
}

func (s *stubRenderer) FramesRendered() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames
}

func (s *stubRenderer) FrameSize() (w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.videoW, s.videoH
}

func (s *stubRenderer) ImageSource() ui.ImageSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src
}

func (s *stubRenderer) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *stubRenderer) setSource(src ui.ImageSource, w, h int) {
	s.mu.Lock()
	s.src = src
	s.videoW = w
	s.videoH = h
	s.frames++
	s.mu.Unlock()
}

func (s *stubRenderer) lastViewport() (w, h, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewportW, s.viewportH, s.viewportCalls
}

func makeSplitBitmap(w, h int, top, bottom color.RGBA) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		c := top
		if y >= h/2 {
			c = bottom
		}
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return ui.NewBitmap(img)
}

func TestViewNilPlayer(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		View(c, nil).Fill()
	}, 120, 80)
	tt.Frame()

	img := tt.Image()
	if img == nil {
		t.Fatal("tt.Image() returned nil")
	}
	if b := img.Bounds(); b.Dx() != 120 || b.Dy() != 80 {
		t.Fatalf("rendered bounds = %dx%d, want 120x80", b.Dx(), b.Dy())
	}
	for _, pt := range [][2]int{{0, 0}, {60, 40}, {119, 79}} {
		got := img.RGBAAt(pt[0], pt[1])
		if got.R != 0 || got.G != 0 || got.B != 0 || got.A != 255 {
			t.Fatalf("pixel (%d,%d) = %+v, want opaque black (0,0,0,255)", pt[0], pt[1], got)
		}
	}
}

func TestViewBindsInvalidateHook(t *testing.T) {
	stub := &stubRenderer{}
	p := &Player{
		renderer: stub,
		snap:     Snapshot{State: StatePlaying},
	}

	tt := ui.NewTester(func(c *ui.Context) {
		View(c, p).Fill()
	}, 160, 120)
	tt.Frame()

	p.mu.RLock()
	hasInv := p.invalidate != nil
	p.mu.RUnlock()
	if !hasInv {
		t.Fatal("expected View to bind window Invalidate hook onto Player")
	}
	if tt.FramePending() {
		t.Fatal("expected FramePending() == false immediately after Frame()")
	}

	p.notifyFrame()
	if !tt.FramePending() {
		t.Fatal("expected p.notifyFrame() to trigger window Invalidate (FramePending() == true)")
	}

	tt.Frame()
	if tt.FramePending() {
		t.Fatal("expected FramePending() == false after settling invalidated frame")
	}
}

func TestViewReportsPhysicalViewportSizeEvenPixels(t *testing.T) {
	stub := &stubRenderer{}
	p := &Player{
		renderer: stub,
		snap:     Snapshot{State: StatePlaying},
	}

	// Start with odd logical dimensions at 1x scale: 161x121 -> 160x120.
	tt := ui.NewTester(func(c *ui.Context) {
		View(c, p).Fill()
	}, 161, 121)
	tt.Frame()

	w, h, calls := stub.lastViewport()
	if calls == 0 || w != 160 || h != 120 {
		t.Fatalf("1x odd viewport: got (%d,%d) calls=%d, want (160,120) calls>0", w, h, calls)
	}

	// HiDPI 2x scale: 160x120 DIP -> 320x240 physical pixels.
	tt.SetSize(160, 120)
	tt.SetScale(2.0)
	tt.Frame()

	w, h, _ = stub.lastViewport()
	if w != 320 || h != 240 {
		t.Fatalf("2x viewport: got (%d,%d), want (320,240)", w, h)
	}

	// Fractional DPI 1.25x on 100x80 DIP -> 125x100 -> rounded down to even 124x100.
	tt.SetSize(100, 80)
	tt.SetScale(1.25)
	tt.Frame()

	w, h, _ = stub.lastViewport()
	if w != 124 || h != 100 {
		t.Fatalf("1.25x fractional viewport: got (%d,%d), want (124,100)", w, h)
	}
}

func TestViewZeroSizeDoesNotReportViewport(t *testing.T) {
	stub := &stubRenderer{}
	p := &Player{
		renderer: stub,
		snap:     Snapshot{State: StatePlaying},
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			View(c, p).Width(0).Height(0)
		})
	}, 160, 120)
	tt.Frame()

	if w, h, calls := stub.lastViewport(); calls != 0 {
		t.Fatalf("zero-sized View called SetViewportSize(%d,%d) %d times, want 0", w, h, calls)
	}
}

func TestViewOverlayCompositing(t *testing.T) {
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}

	stub := &stubRenderer{}
	stub.setSource(makeSplitBitmap(160, 120, red, blue), 160, 120)
	p := &Player{
		renderer: stub,
		snap:     Snapshot{State: StatePlaying},
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			View(c, p).Fill()
			ui.Box(c).Absolute().Left(70).Top(50).Width(20).Height(20).Background(ui.RGB(0, 255, 0))
			ui.Text(c, "Overlay HUD").Absolute().Left(8).Top(8).TextColor(ui.RGB(255, 255, 255))
		})
	}, 160, 120)
	tt.Frame()

	if !tt.HasText("Overlay HUD") {
		t.Fatal("expected overlay text 'Overlay HUD' to be present in scene")
	}
	img := tt.Image()
	topC := img.RGBAAt(120, 25)
	botC := img.RGBAAt(120, 95)
	midC := img.RGBAAt(80, 60)
	if topC.R < 220 || topC.G > 20 || topC.B > 20 {
		t.Fatalf("frame 1 top expected RED, got %+v", topC)
	}
	if botC.B < 220 || botC.R > 20 || botC.G > 20 {
		t.Fatalf("frame 1 bottom expected BLUE, got %+v", botC)
	}
	if midC.G < 220 || midC.R > 20 || midC.B > 20 {
		t.Fatalf("frame 1 center overlay expected GREEN, got %+v", midC)
	}

	// Swap frame to inverted colors, notify UI, and confirm frame + overlay re-composite cleanly.
	stub.setSource(makeSplitBitmap(160, 120, blue, red), 160, 120)
	p.notifyFrame()
	if !tt.FramePending() {
		t.Fatal("expected FramePending() after frame update")
	}
	tt.Frame()

	img2 := tt.Image()
	topC2 := img2.RGBAAt(120, 25)
	botC2 := img2.RGBAAt(120, 95)
	midC2 := img2.RGBAAt(80, 60)
	if topC2.B < 220 || topC2.R > 20 || topC2.G > 20 {
		t.Fatalf("frame 2 top expected BLUE, got %+v", topC2)
	}
	if botC2.R < 220 || botC2.G > 20 || botC2.B > 20 {
		t.Fatalf("frame 2 bottom expected RED, got %+v", botC2)
	}
	if midC2.G < 220 || midC2.R > 20 || midC2.B > 20 {
		t.Fatalf("frame 2 center overlay expected GREEN, got %+v", midC2)
	}
}

func TestViewIdleAndClosedPlayerNoPanic(t *testing.T) {
	t.Run("idle without renderer", func(t *testing.T) {
		p := &Player{snap: Snapshot{State: StateIdle}}
		tt := ui.NewTester(func(c *ui.Context) {
			View(c, p).Fill()
		}, 120, 80)
		tt.Frame()

		got := tt.Image().RGBAAt(60, 40)
		if got.R != 0 || got.G != 0 || got.B != 0 || got.A != 255 {
			t.Fatalf("idle player background = %+v, want opaque black", got)
		}
	})

	t.Run("idle with nil frame source", func(t *testing.T) {
		stub := &stubRenderer{src: nil}
		p := &Player{
			renderer: stub,
			snap:     Snapshot{State: StateIdle},
		}
		tt := ui.NewTester(func(c *ui.Context) {
			View(c, p).Fill()
		}, 120, 80)
		tt.Frame()

		got := tt.Image().RGBAAt(60, 40)
		if got.R != 0 || got.G != 0 || got.B != 0 || got.A != 255 {
			t.Fatalf("idle nil-source background = %+v, want opaque black", got)
		}
		if w, h, calls := stub.lastViewport(); calls == 0 || w != 120 || h != 80 {
			t.Fatalf("idle viewport = (%d,%d) calls=%d, want (120,80) calls>0", w, h, calls)
		}
	})

	t.Run("closed player ignores viewport updates and frame notifications", func(t *testing.T) {
		stub := &stubRenderer{}
		p := &Player{
			renderer: stub,
			snap:     Snapshot{State: StateIdle},
		}
		p.closing.Store(true)
		p.closed.Store(true)

		tt := ui.NewTester(func(c *ui.Context) {
			View(c, p).Fill()
		}, 120, 80)
		tt.Frame()

		if w, h, calls := stub.lastViewport(); calls != 0 {
			t.Fatalf("closed player called SetViewportSize(%d,%d) %d times, want 0", w, h, calls)
		}
		p.notifyFrame()
		if tt.FramePending() {
			t.Fatal("closed player notifyFrame() should not invalidate window")
		}
	})
}
