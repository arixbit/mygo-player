package player

import (
	"math"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var (
	scaleMu     sync.RWMutex
	cachedScale float64
	watchOnce   sync.Once
)

// View renders p's zero-copy GPU texture inside MyGo's native UI scene graph.
//
// The returned ui.Element can be styled and laid out like any ui.Image
// (Fill, Width, Height, Fit, Radius, Opacity, etc.). View automatically binds
// the window's Invalidate hook to the player and updates the GPU render target
// resolution to match the element's physical Retina pixel size so both video
// and libass subtitles stay sharp.
func View(c *ui.Context, p *Player) ui.Element {
	if p == nil {
		return ui.Box(c).Background(ui.RGB(0, 0, 0))
	}
	svc := c.Services()
	p.setInvalidate(svc.Invalidate)

	el := ui.Image(c, p.Texture()).Fit(ui.Contain).Background(ui.RGB(0, 0, 0))
	if b := el.Bounds(); b.W > 0 && b.H > 0 {
		scale := effectiveScale(c)
		pw := int(math.Round(float64(b.W) * scale))
		ph := int(math.Round(float64(b.H) * scale))
		// Round to even dimensions to keep video/GPU scaling clean.
		pw &^= 1
		ph &^= 1
		if pw > 0 && ph > 0 {
			p.SetRenderSize(pw, ph)
		}
	}
	return el
}

func effectiveScale(c *ui.Context) float64 {
	scale := queryDisplayScale()
	if z := float64(c.Zoom()); z > 0 {
		scale *= z
	}
	if scale <= 0 {
		return 1.0
	}
	return scale
}

func queryDisplayScale() float64 {
	scaleMu.RLock()
	s := cachedScale
	scaleMu.RUnlock()
	if s > 0 {
		return s
	}

	if !mygo.App.IsReady() {
		return 1.0
	}

	d := mygo.Screen.PrimaryDisplay()
	if d.ScaleFactor <= 0 {
		return 1.0
	}

	scaleMu.Lock()
	cachedScale = d.ScaleFactor
	scaleMu.Unlock()

	watchOnce.Do(func() {
		mygo.Screen.OnDisplaysChanged(func() {
			if nd := mygo.Screen.PrimaryDisplay(); nd.ScaleFactor > 0 {
				scaleMu.Lock()
				cachedScale = nd.ScaleFactor
				scaleMu.Unlock()
			}
		})
	})

	return d.ScaleFactor
}
