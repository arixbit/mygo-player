package player

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// View renders p's video frame inside MyGo's native UI scene graph (zero-copy
// Metal *native.Texture on macOS; double-buffered *ui.Bitmap on Windows and
// Linux). Because frames are composited directly in MyGo rather than a native
// child window, overlays such as danmaku, HUD controls, rounded corners, and
// popovers always render cleanly on top.
//
// The returned ui.Element can be styled and laid out like any ui.Image
// (Fill, Width, Height, Radius, Opacity, etc.). View automatically binds
// the window's Invalidate hook to the player and reads the window's live
// display scale via ui.Painter.Scale() so both video and libass subtitles stay
// sharp across HiDPI/Retina and multi-monitor setups.
//
// Aspect ratio and filling: View asks libmpv to render at the element's full
// physical size, and libmpv itself letterboxes the picture inside that frame
// (black bars) while drawing subtitles at full viewport resolution. The
// element's Fit(ui.Contain) therefore only matters before the first sized
// frame; overriding it with Fit(ui.Cover) does NOT remove libmpv's black bars
// (the frame already matches the element's size, bars included). To fill the viewport (crop-to-fill), keep the
// View full-size and call p.SetPanscan(1.0) (any value in 0..1 trades bars
// for cropping); SetVideoRotate rotates within the same frame.
func View(c *ui.Context, p *Player) ui.Element {
	if p == nil {
		return ui.Box(c).Background(ui.RGB(0, 0, 0))
	}
	svc := c.Services()
	p.setInvalidate(svc.Invalidate)

	return ui.Image(c, p.ImageSource()).Fit(ui.Contain).Background(ui.RGB(0, 0, 0)).Draw(func(pt *ui.Painter, r ui.Rect) {
		if r.W <= 0 || r.H <= 0 {
			return
		}
		scale := float64(pt.Scale())
		if scale <= 0 {
			scale = 1.0
		}
		pw := int(math.Round(float64(r.W) * scale))
		ph := int(math.Round(float64(r.H) * scale))
		// Round to even dimensions to keep video/GPU scaling clean.
		pw &^= 1
		ph &^= 1
		if pw > 0 && ph > 0 {
			p.SetRenderSize(pw, ph)
		}
	})
}
