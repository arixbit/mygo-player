package player

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// View renders p's zero-copy GPU texture inside MyGo's native UI scene graph.
//
// The returned ui.Element can be styled and laid out like any ui.Image
// (Fill, Width, Height, Fit, Radius, Opacity, etc.). View automatically binds
// the window's Invalidate hook to the player and reads the window's live
// display scale via ui.Painter.Scale() so both video and libass subtitles stay
// sharp across Retina and multi-monitor setups.
func View(c *ui.Context, p *Player) ui.Element {
	if p == nil {
		return ui.Box(c).Background(ui.RGB(0, 0, 0))
	}
	svc := c.Services()
	p.setInvalidate(svc.Invalidate)

	return ui.Image(c, p.Texture()).Fit(ui.Contain).Background(ui.RGB(0, 0, 0)).Draw(func(pt *ui.Painter, r ui.Rect) {
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
