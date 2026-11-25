package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	player "github.com/arixbit/mygo-player"
)

// Colors of the controls over the picture. They are the same in light and
// dark appearance: the picture behind them is what they must stand out from.
var (
	hudText     = ui.RGB(245, 245, 247)
	hudMuted    = ui.RGBA(235, 235, 245, 0.62)
	hudTrack    = ui.RGBA(255, 255, 255, 0.24)
	hudHover    = ui.RGBA(255, 255, 255, 0.12)
	hudPressed  = ui.RGBA(255, 255, 255, 0.2)
	hudHairline = ui.RGBA(255, 255, 255, 0.1)
	hudDanger   = ui.RGB(255, 105, 97)
	hudShadow   = ui.RGBA(0, 0, 0, 0.35)
	transparent = ui.Transparent
	stageBlack  = ui.RGB(0, 0, 0)
)

// hudSurface is the fill of panels over the picture: translucent, unless
// the desktop asks for less transparency.
func hudSurface(c *ui.Context) ui.Color {
	if c.Preferences().ReduceTransparency {
		return ui.RGB(32, 32, 36)
	}
	return ui.RGBA(30, 30, 34, 0.86)
}

// layout holds the sizes the window allows this frame.
type layout struct {
	w, h   float32
	margin float32 // from the window edges
	button float32 // control size: larger for touch
	panelW float32 // width of the control bar
}

func newLayout(c *ui.Context) layout {
	w, h := c.Size()
	l := layout{w: w, h: h, margin: 16, button: 36}
	if w < 480 {
		l.margin = 10
	}
	if c.Preferences().Pointer == ui.PointerCoarse {
		l.button = 44
	}
	l.panelW = min(w-2*l.margin, 760)
	return l
}

// fits reports whether the control bar is at least w wide at the 36 DIP
// control size, scaling up for touch-sized controls.
func (l layout) fits(w float32) bool { return l.panelW >= w*l.button/36 }

func (a *demoApp) view(c *ui.Context) {
	now := c.Now()
	snap := a.p.Snapshot()
	a.syncWindow(snap)
	for _, t := range a.pending {
		c.AddToast(t)
	}
	a.pending = nil

	l := newLayout(c)
	has := a.hasMedia(snap)
	failed := a.target != "" && snap.State == player.StateError
	a.shortcuts(c, snap, has, now)

	root := ui.Box(c.Key("stage")).Fill()
	root.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerMove, ui.InputPointerDown, ui.InputScroll:
			a.lastTouch = ev.Pointer == ui.PointerTouch
			if !a.lastTouch {
				a.poke(ev.Time)
			}
		}
		return false
	})
	if dropped := root.DroppedFiles(); len(dropped) > 0 {
		a.openPaths(dropped)
		snap = a.p.Snapshot()
		has = a.hasMedia(snap)
	}
	dragOver := root.FileDragOver()

	// The controls show while the user is doing something, and whenever
	// they are what the user needs: paused, ended, in a menu, dragging.
	pinned := snap.Paused || snap.State == player.StateEnded || a.anyMenuOpen() ||
		a.scrubbing || a.volDragging || a.hudHot || dragOver
	idleFor := now.Sub(a.lastActivity)
	chrome := has && (pinned || idleFor < hideChromeAfter)
	if has && !pinned && chrome {
		c.After(hideChromeAfter - idleFor)
	}
	a.chromeShown = chrome

	bg := c.Theme().Background
	if has || failed {
		bg = stageBlack
	}
	bg = root.AnimateColor("bg", bg, motionStage)
	root.Background(bg)

	root.Children(func() {
		a.canvas(c).Fill()
		// The cover hides the black video surface until there is a
		// picture, and fades away to reveal its first frame.
		cover := float32(0)
		if has && snap.VideoWidth > 0 && snap.State != player.StateLoading {
			cover = 1
		}
		ui.Box(c.Key("cover")).Absolute().Fill().Background(bg).IgnoreInput(true).
			AnimatePose(ui.Pose{Fade: cover}, motionStage)

		a.hitLayer(c, has, chrome)
		a.liftSubtitles(c, root, snap, has, chrome, l)

		// Things in the middle of the stage.
		ui.Box(c.Key("center")).Absolute().Fill().Center().Padding(l.margin).PassThrough().Children(func() {
			switch {
			case !has && !failed:
				a.welcome(c, dragOver, l)
			}
		})
		ui.Themed(c, a.hudTheme(c)).Absolute().Fill().Background(ui.Transparent).PassThrough().Children(func() {
			ui.Box(c.Key("hud-center")).Absolute().Fill().Center().Padding(l.margin).PassThrough().Children(func() {
				switch {
				case failed:
					a.errorCard(c, snap, l)
				case has && (snap.State == player.StateLoading || snap.Buffering):
					a.loadingPill(c, snap)
				}
			})
			a.titleBar(c, snap, has && chrome && a.fullScreen(), l)
			a.controlBar(c, snap, has, chrome, now, l)
			if has && dragOver {
				a.dropTarget(c, l)
			}
		})
	})
	a.toasts(c)
}

// hudTheme is the dark theme the controls over the picture use, whatever
// the appearance, following the desktop's other preferences.
func (a *demoApp) hudTheme(c *ui.Context) *ui.Theme {
	prefs := c.Preferences()
	if a.dark == nil || prefs != a.darkPrefs {
		a.dark, a.darkPrefs = ui.DarkTheme().WithPreferences(prefs), prefs
	}
	return a.dark
}

func (a *demoApp) shortcuts(c *ui.Context, s player.Snapshot, has bool, now time.Time) {
	if c.FocusTakesText() {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyO) && !c.ShortcutRepeated() {
		a.chooseVideo()
	}
	if !has || a.anyMenuOpen() {
		return
	}
	acted := true
	switch {
	case (c.Shortcut(0, ui.KeySpace) || c.Shortcut(0, ui.KeyK)) && !c.ShortcutRepeated():
		a.togglePlay()
	case c.Shortcut(0, ui.KeyLeft):
		a.skip(-skipStep)
	case c.Shortcut(0, ui.KeyRight):
		a.skip(skipStep)
	case c.Shortcut(0, ui.KeyUp):
		a.changeVolume(s, volumeStep)
	case c.Shortcut(0, ui.KeyDown):
		a.changeVolume(s, -volumeStep)
	case c.Shortcut(0, ui.KeyM) && !c.ShortcutRepeated():
		a.toggleMute()
	case c.Shortcut(0, ui.KeyC) && !c.ShortcutRepeated():
		a.toggleSubtitles(s)
	case c.Shortcut(0, ui.KeyF) && !c.ShortcutRepeated():
		a.toggleFullScreen()
	case a.fullScreen() && c.Shortcut(0, ui.KeyEscape):
		a.toggleFullScreen()
	default:
		acted = false
	}
	if acted {
		a.lastActivity = now // show what the key did
	}
}

// hitLayer takes clicks on the picture: a click plays or pauses, a double
// click toggles full screen. With touch, a tap shows or hides the controls.
func (a *demoApp) hitLayer(c *ui.Context, has, chrome bool) {
	hit := ui.Box(c.Key("hit")).Absolute().Fill().AccessHidden(true)
	if !has {
		return
	}
	if !chrome && c.Preferences().Hover() {
		hit.Cursor(ui.CursorNone) // out of the picture's way with the controls
	}
	double, click := hit.DoubleClicked(), hit.Clicked()
	switch {
	case double && !a.lastTouch:
		a.togglePlay() // undo the first click's toggle
		a.toggleFullScreen()
	case click && a.lastTouch:
		if chrome {
			a.lastActivity = time.Time{}
		} else {
			a.lastActivity = time.Now()
		}
	case click:
		a.togglePlay()
	}
}

// liftSubtitles moves subtitles up out of the way of the control bar while
// it shows, along with it, and back down as it hides.
func (a *demoApp) liftSubtitles(c *ui.Context, root ui.Element, s player.Snapshot, has, chrome bool, l layout) {
	target := float32(100)
	if has && chrome && l.h > 0 {
		hud := l.margin + 2*10 + 20 + 4 + l.button + 8
		target = 100 - float32(math.Ceil(float64(hud/l.h*100)))
	}
	anim := motionChromeIn
	if !chrome {
		anim = motionChromeOut
	}
	pos := int(math.Round(float64(root.AnimateTo("sub-pos", target, anim))))
	if pos != a.subPosSet && (has || a.subPosSet != 0) {
		a.subPosSet = pos
		a.applySubPos(max(0, min(pos, 150)))
	}
}

func (a *demoApp) welcome(c *ui.Context, dragOver bool, l layout) {
	t := c.Theme()
	border, icon, iconColor, title := t.Border, iconFilm, t.TextMuted, "Drop a Video Here"
	scale := float32(1)
	if dragOver {
		border, icon, iconColor, title, scale = t.Accent, iconDrop, t.Accent, "Drop to Play", 1.03
	}
	card := ui.Column(c.Key("welcome")).Center().Gap(10).Padding(32, 40).Radius(20).
		MaxWidth(min(440, l.w-2*l.margin)).Border(1.5, border).BorderStyle(ui.BorderDashed).
		Transition(overlayTransition)
	card.AnimatePose(ui.Pose{Scale: scale}, motionPress)
	card.Children(func() {
		ui.Icon(c, icon).FontSize(44).TextColor(iconColor).ContentTransition(contentSwap)
		ui.Text(c, title).FontSize(20).Bold().TextColor(t.Text).TextAlign(ui.Center).ContentTransition(contentSwap)
		ui.Text(c, "MKV, MP4, WebM and more. Subtitles with the same name load on their own.").
			TextColor(t.TextMuted).TextAlign(ui.Center)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
			b := ui.PrimaryButton(c.Key("choose"), "Choose Video…").KeyShortcuts(ui.Cmd, ui.KeyO)
			if b.Clicked() {
				a.chooseVideo()
			}
			ui.Text(c, shortcutLabel("O")).FontSize(12).TextColor(t.TextMuted).FontFeatures("tnum")
		})
	})
}

func (a *demoApp) errorCard(c *ui.Context, s player.Snapshot, l layout) {
	reason := "This file can’t be played."
	if s.Err != nil {
		reason = friendlyError(s.Err)
	}
	ui.Column(c.Key("error")).Center().Gap(8).Padding(24, 28).Radius(18).
		MaxWidth(min(440, l.w-2*l.margin)).Background(hudSurface(c)).Border(1, hudHairline).
		Shadow(0, 12, 40, 0, hudShadow).Transition(overlayTransition).Children(func() {
		ui.Icon(c, iconAlert).FontSize(32).TextColor(hudDanger)
		ui.Text(c, "Can’t Play This Video").FontSize(17).Bold().TextColor(hudText).TextAlign(ui.Center)
		ui.Text(c, mediaName("", a.target)).FontSize(13).TextColor(hudText).TextAlign(ui.Center).SingleLine().TruncateMiddle()
		ui.Text(c, reason).FontSize(13).TextColor(hudMuted).TextAlign(ui.Center)
		b := ui.PrimaryButton(c.Key("choose-other"), "Choose Another Video…").Margin(8, 0, 0, 0)
		if b.Clicked() {
			a.chooseVideo()
		}
	})
}

func (a *demoApp) loadingPill(c *ui.Context, s player.Snapshot) {
	label := "Loading…"
	if s.State != player.StateLoading {
		label = "Buffering…"
	}
	ui.Row(c.Key("loading")).Gap(10).AlignItems(ui.Center).Padding(10, 16).Radius(999).
		Background(hudSurface(c)).Border(1, hudHairline).Transition(loadingTransition).
		IgnoreInput(true).Children(func() {
		ui.Spinner(c).Size(16, 16)
		ui.Text(c, label).FontSize(13).TextColor(hudText).ContentTransition(contentSwap)
	})
}

// titleBar names what plays in full screen, where the window's title bar
// that names it otherwise is out of sight.
func (a *demoApp) titleBar(c *ui.Context, s player.Snapshot, shown bool, l layout) {
	pose, anim := ui.Pose{}, motionChromeIn
	if !shown {
		pose, anim = ui.Pose{Y: -12, Fade: 1}, motionChromeOut
	}
	ui.Row(c.Key("title")).Absolute().Top(l.margin).Left(l.margin).MaxWidth(l.w*0.6).
		Padding(8, 14).Radius(12).Background(hudSurface(c)).Border(1, hudHairline).
		IgnoreInput(true).AccessHidden(!shown).AnimatePose(pose, anim).Children(func() {
		ui.Text(c, mediaName(s.MediaTitle, a.target)).FontSize(13).Bold().TextColor(hudText).
			SingleLine().TruncateMiddle().ContentTransition(contentSwap)
	})
}

func (a *demoApp) dropTarget(c *ui.Context, l layout) {
	ui.Column(c.Key("drop")).Absolute().Top(l.margin).Left(l.margin).Right(l.margin).Bottom(l.margin).
		Center().Gap(8).Padding(24).Radius(20).Background(ui.RGBA(0, 0, 0, 0.6)).
		Border(2, c.Theme().Accent).BorderStyle(ui.BorderDashed).IgnoreInput(true).
		Transition(overlayTransition).Children(func() {
		ui.Icon(c, iconDrop).FontSize(36).TextColor(hudText)
		ui.Text(c, "Drop to Open").FontSize(18).Bold().TextColor(hudText)
		ui.Text(c, "A video replaces what’s playing. Subtitle files are added to it.").
			FontSize(13).TextColor(hudMuted).TextAlign(ui.Center)
	})
}

func (a *demoApp) toasts(c *ui.Context) {
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Justify(ui.Start).AlignItems(ui.Center).Padding(16).Gap(8)
		for _, t := range toasts {
			tp := ui.ToastBase(c, t)
			tp.Root.Row().Gap(10).AlignItems(ui.Start).Padding(10, 14).Radius(14).MaxWidth(460).
				Background(hudSurface(c)).Border(1, hudHairline).Shadow(0, 10, 30, 0, hudShadow).
				Transition(toastTransition).Children(func() {
				if t.Type == "error" {
					ui.Icon(c, iconAlert).FontSize(18).TextColor(hudDanger)
				}
				ui.Column(c).Gap(2).Shrink(1).Children(func() {
					ui.Text(c, t.Title).FontSize(13).Bold().TextColor(hudText)
					if t.Description != "" {
						ui.Text(c, t.Description).FontSize(12).TextColor(hudMuted)
					}
				})
			})
		}
	})
}
