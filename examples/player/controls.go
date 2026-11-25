package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	player "github.com/arixbit/mygo-player"
)

// controlBar is the floating bar of playback controls: the seek bar above,
// volume on the left, transport in the middle, and what the media offers
// (speed, audio tracks, subtitles) with full screen on the right. Controls
// that the window has no room for, or that would do nothing for this media,
// are left out rather than squeezed or disabled.
func (a *demoApp) controlBar(c *ui.Context, s player.Snapshot, has, shown bool, now time.Time, l layout) {
	pose, anim := ui.Pose{}, motionChromeIn
	if !shown {
		pose, anim = ui.Pose{Y: 12, Fade: 1}, motionChromeOut
	}
	ui.Row(c.Key("hud-wrap")).Absolute().Left(0).Right(0).Bottom(l.margin).PaddingX(l.margin).
		Justify(ui.Center).PassThrough().Children(func() {
		hud := ui.Column(c.Key("hud")).Direction(ui.LeftToRight).Grow(1).MaxWidth(760).Padding(10, 14).Gap(4).Radius(18).
			Background(hudSurface(c)).Border(1, hudHairline).Shadow(0, 10, 36, 0, hudShadow).
			// A click on a control leaves the keyboard focus where it was,
			// so Space keeps playing and pausing rather than pressing the
			// last button clicked again.
			KeepFocus().IgnoreInput(!shown).AccessHidden(!shown).Label("Playback controls")
		hud.AnimatePose(pose, anim)
		hot := shown && (hud.Hovered() || hud.FocusWithin())
		if hot != a.hudHot {
			a.hudHot = hot
			c.Invalidate()
		}
		if !has {
			a.resetControls()
			return
		}
		hud.Children(func() {
			a.seekRow(c, s, now)
			a.buttonRow(c, s, l, now)
		})
	})
}

// resetControls forgets the state of controls the frame did not build, as
// a menu or a slider the window grew too narrow for while in use: their
// release never comes.
func (a *demoApp) resetControls() {
	a.speedOpen, a.audioOpen, a.subOpen, a.moreOpen = false, false, false, false
	a.scrubbing, a.volDragging = false, false
}

func (a *demoApp) seekRow(c *ui.Context, s player.Snapshot, now time.Time) {
	dur := s.Duration.Seconds()
	pos := s.Position.Seconds()
	// Until playback reports the position the user sought to, keep showing
	// that position rather than the stale one.
	if !a.scrubbing && !a.holdUntil.IsZero() {
		if now.After(a.holdUntil) || absDiff(pos, a.scrubPos) < 0.75 {
			a.holdUntil = time.Time{}
		} else {
			pos = a.scrubPos
		}
	}
	if a.scrubbing {
		pos = a.scrubPos
	}
	long := s.Duration >= time.Hour
	clockW := float32(40)
	if long {
		clockW = 56
	}
	shownPos := time.Duration(pos * float64(time.Second))
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, formatClock(shownPos, long)).FontSize(12).FontFeatures("tnum").TextColor(hudMuted).
			Width(clockW).SingleLine()
		a.seekBar(c, s, pos, dur, now)
		remaining := ""
		if seekable(s) {
			remaining = formatRemaining(shownPos, s.Duration) // unknown for live streams
		}
		ui.Text(c, remaining).FontSize(12).FontFeatures("tnum").TextColor(hudMuted).
			Width(clockW + 6).TextAlign(ui.End).SingleLine()
	})
}

// seekBar follows the pointer while dragged, seeking along at a steady
// rate and exactly where it is let go; hovering previews the time under
// the pointer.
func (a *demoApp) seekBar(c *ui.Context, s player.Snapshot, pos, dur float64, now time.Time) {
	const pad = 6
	val := pos
	seekable := dur > 0
	if !seekable {
		a.scrubbing = false
	}
	ui.Box(c.Key("seek-wrap")).Grow(1).Height(20).Children(func() {
		bar := ui.SliderBase(c.Key("seek"), &val, 0, max(dur, 0.001)).Fill().PaddingX(pad).
			Label("Playback position").Disabled(!seekable)
		// The slider keeps the press wherever the pointer goes; Pressed
		// alone ends as the pointer leaves the bar.
		btn, held := bar.PressedButton()
		pressed := held && btn == 0
		if bar.Changed() {
			a.scrubPos = val
			if pressed {
				a.scrubbing = true
				if now.Sub(a.lastScrubSeek) >= scrubSeekEvery {
					a.lastScrubSeek = now
					a.seek(val)
				}
			} else {
				a.seek(val) // arrows, Page Up/Down, Home/End
				a.holdUntil = now.Add(seekHold)
			}
		}
		if a.scrubbing && !pressed {
			a.scrubbing = false
			a.seek(a.scrubPos) // exactly where it was let go
			a.holdUntil = now.Add(seekHold)
		}

		hot := seekable && (bar.Hovered() || pressed || bar.Focused())
		th := bar.AnimateTo("track", pick(hot, 6, 4), motionTrack)
		knob := bar.AnimateTo("knob", pick(hot, 1, 0), motionTrack)
		frac := float32(0)
		if seekable {
			frac = float32(min(max(val/dur, 0), 1))
		}
		bar.Draw(func(p *ui.Painter, r ui.Rect) {
			drawTrack(p, r, pad, th, knob, frac, hudText)
		})

		// The time preview: under the pointer while hovering, at the
		// thumb while dragging.
		px, _, over := bar.PointerPosition()
		w := bar.Bounds().W - 2*pad
		if !seekable || w <= 0 || !(over || pressed) {
			return
		}
		x, at := pad+w*frac, val
		if !pressed {
			f := min(max((px-pad)/w, 0), 1)
			x, at = pad+w*f, float64(f)*dur
		}
		ui.Text(c.Key("seek-tip"), formatClock(secs(at), dur >= 3600)).Absolute().Left(x).Bottom(24).
			TranslatePercent(-50, 0).Padding(3, 7).Radius(7).Background(tipSurface(c)).
			FontSize(12).FontFeatures("tnum").TextColor(hudText).SingleLine().IgnoreInput(true).
			AccessHidden(true).Transition(tipTransition)
	})
}

func tipSurface(c *ui.Context) ui.Color {
	if c.Preferences().ReduceTransparency {
		return ui.RGB(20, 20, 24)
	}
	return ui.RGBA(20, 20, 24, 0.92)
}

// drawTrack paints a slider's track, filled up to frac, with a knob that
// grows in as knob goes from 0 to 1.
func drawTrack(p *ui.Painter, r ui.Rect, pad, th, knob, frac float32, fill ui.Color) {
	x0, w := r.X+pad, r.W-2*pad
	cy := r.Y + r.H/2
	p.Fill(ui.Rect{X: x0, Y: cy - th/2, W: w, H: th}, hudTrack, th/2)
	if frac > 0 {
		p.Fill(ui.Rect{X: x0, Y: cy - th/2, W: max(w*frac, th), H: th}, fill, th/2)
	}
	if knob > 0.01 {
		d := 12 * knob
		p.Fill(ui.Rect{X: x0 + w*frac - d/2, Y: cy - d/2, W: d, H: d}, ui.RGB(255, 255, 255), d/2)
	}
}

// barFit decides which controls the bar has room for, giving up the least
// needed first: the volume slider (the mute button stays), the skip buttons
// (the arrow keys stay), then the speed button, which moves into the More
// menu, as does the audio track button last of all.
type barFit struct{ volume, skip, speed, audio bool }

func fitBar(l layout, audioMenu bool) barFit {
	b := l.button
	side := func(f barFit) float32 {
		left := b
		if f.volume {
			left += 2 + 88
		}
		right := 3 * b // subtitles, more, full screen
		if audioMenu && f.audio {
			right += b
		}
		if f.speed {
			right += b + 16
		}
		return max(left, right)
	}
	center := func(f barFit) float32 {
		if f.skip {
			return 3*b + 6 + 12
		}
		return b + 6
	}
	for _, f := range []barFit{{true, true, true, true}, {false, true, true, true}, {false, false, true, true}, {false, false, false, true}, {false, false, false, false}} {
		if 2*side(f)+center(f)+28 <= l.panelW {
			return f
		}
	}
	return barFit{}
}

func (a *demoApp) buttonRow(c *ui.Context, s player.Snapshot, l layout, now time.Time) {
	audio := filterByType(s.Tracks, player.TrackAudio)
	subs := filterByType(s.Tracks, player.TrackSub)
	fit := fitBar(l, len(audio) > 1)
	fit.skip = fit.skip && seekable(s)
	if !fit.speed {
		a.speedOpen = false
	}
	audioMenu := len(audio) > 1 && fit.audio
	if !audioMenu {
		a.audioOpen = false
	}
	if !fit.volume {
		a.volDragging = false
	}

	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Width(0).Grow(1).AlignItems(ui.Center).Gap(2).Children(func() {
			a.volumeControls(c, s, l, fit.volume, now)
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if fit.skip {
				b := a.iconButton(c, "back", iconBack, "Back 5 Seconds", "Back 5 seconds (←)", l.button, 20).
					KeyShortcuts(0, ui.KeyLeft)
				if b.Clicked() {
					a.skip(s, -skipStep)
				}
			}
			icon, label := iconPause, "Pause"
			switch {
			case s.State == player.StateEnded || s.EOF:
				icon, label = iconReplay, "Replay"
			case s.Paused:
				icon, label = iconPlay, "Play"
			}
			b := a.iconButton(c, "play", icon, label, label+" (Space)", l.button+6, 24).KeyShortcuts(0, ui.KeySpace)
			if a.focusPlay {
				a.focusPlay = false
				b.Focus()
			}
			if b.Clicked() {
				a.togglePlay()
			}
			if fit.skip {
				b := a.iconButton(c, "forward", iconForward, "Forward 5 Seconds", "Forward 5 seconds (→)", l.button, 20).
					KeyShortcuts(0, ui.KeyRight)
				if b.Clicked() {
					a.skip(s, skipStep)
				}
			}
		})
		ui.Row(c).Width(0).Grow(1).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if fit.speed {
				a.speedMenu(c, s, l)
			}
			if audioMenu {
				a.audioMenu(c, audio, l)
			}
			a.subtitleMenu(c, s, subs, l)
			var moreAudio []player.Track
			if len(audio) > 1 && !audioMenu {
				moreAudio = audio
			}
			a.moreMenu(c, s, l, !fit.speed, moreAudio)
			icon, label := iconEnterFullScreen, "Full Screen"
			if a.fullScreen() {
				icon, label = iconExitFullScreen, "Exit Full Screen"
			}
			if a.iconButton(c, "fullscreen", icon, label, label+" (F)", l.button, 18).KeyShortcuts(0, ui.KeyF).Clicked() {
				a.toggleFullScreen()
			}
		})
	})
}

func (a *demoApp) volumeControls(c *ui.Context, s player.Snapshot, l layout, slider bool, now time.Time) {
	icon, label := iconVolumeHigh, "Mute"
	switch {
	case a.silent(s, now):
		icon, label = iconVolumeMuted, "Unmute"
	case a.volume(s, now) < 50:
		icon = iconVolumeLow
	}
	if a.iconButton(c, "mute", icon, label, label+" (M)", l.button, 20).KeyShortcuts(0, ui.KeyM).Clicked() {
		a.toggleMute(s, now)
	}
	if !slider {
		return
	}
	const pad = 6
	v := a.volume(s, now)
	bar := ui.SliderBase(c.Key("volume"), &v, 0, 100).Width(88).Height(20).PaddingX(pad).Label("Volume")
	btn, held := bar.PressedButton()
	pressed := held && btn == 0
	if bar.Changed() {
		a.setVolume(s, v, now)
	}
	a.volDragging = pressed
	hot := bar.Hovered() || pressed || bar.Focused()
	th := bar.AnimateTo("track", pick(hot, 6, 4), motionTrack)
	knob := bar.AnimateTo("knob", pick(hot, 1, 0), motionTrack)
	fill := hudText
	if s.Muted && !pressed {
		fill = hudMuted // the level comes back on unmute
	}
	fill = bar.AnimateColor("fill", fill, motionHover)
	frac := float32(v / 100)
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		drawTrack(p, r, pad, th, knob, frac, fill)
	})
}

func (a *demoApp) speedMenu(c *ui.Context, s player.Snapshot, l layout) {
	if s.Speed != a.lastSpeed {
		a.speedDown = s.Speed < a.lastSpeed
		a.lastSpeed = s.Speed
	}
	b := a.hudButton(c, "speed", "Playback Speed", "Playback speed", l.button).PaddingX(10)
	color := hudText
	if absDiff(s.Speed, 1) > 0.001 {
		color = c.Theme().Accent // not the normal speed: say so
	}
	b.Children(func() {
		ui.Text(c, speedLabel(s.Speed)).FontSize(13).Bold().FontFeatures("tnum").SingleLine().
			TextColor(b.AnimateColor("fg", color, motionHover)).ContentTransition(numericSwap(a.speedDown))
	})
	b.PopoverMenuButton(func(m *ui.Menu) { a.speedItems(m, s) }, a.menuOptions(&a.speedOpen))
}

func (a *demoApp) speedItems(m *ui.Menu, s player.Snapshot) {
	for _, spd := range speeds {
		name := speedLabel(spd)
		if spd == 1 {
			name = "Normal"
		}
		if m.Item(name).ID(fmt.Sprintf("speed-%g", spd)).Checked(absDiff(s.Speed, spd) < 0.001).Chosen() {
			a.setSpeed(spd)
		}
	}
}

// moreMenu holds what has no button of its own: choosing another video,
// which touch has no shortcut or drop for, and the speed and audio tracks
// when the bar is too narrow for their buttons.
func (a *demoApp) moreMenu(c *ui.Context, s player.Snapshot, l layout, speed bool, audio []player.Track) {
	b := a.iconButton(c, "more", iconMore, "More", "More", l.button, 20)
	b.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item("Choose Video…").ID("more-open").Shortcut(ui.Cmd, ui.KeyO).Chosen() {
			a.chooseVideo()
		}
		if speed || len(audio) > 0 {
			m.Separator()
		}
		if speed {
			m.Submenu("Playback Speed", func(m *ui.Menu) { a.speedItems(m, s) })
		}
		if len(audio) > 0 {
			names := trackNames(audio)
			m.Submenu("Audio Track", func(m *ui.Menu) { a.audioItems(m, audio, names) })
		}
	}, a.menuOptions(&a.moreOpen))
}

func (a *demoApp) audioMenu(c *ui.Context, audio []player.Track, l layout) {
	names := trackNames(audio)
	b := a.iconButton(c, "audio", iconAudioTrack, "Audio Track", "Audio track", l.button, 20)
	b.PopoverMenuButton(func(m *ui.Menu) { a.audioItems(m, audio, names) }, a.menuOptions(&a.audioOpen))
}

func (a *demoApp) audioItems(m *ui.Menu, audio []player.Track, names []string) {
	for i, tr := range audio {
		if m.Item(names[i]).ID(fmt.Sprintf("aid-%d", tr.ID)).Checked(tr.Selected).Chosen() {
			a.selectAudio(tr.ID)
		}
	}
}

func (a *demoApp) subtitleMenu(c *ui.Context, s player.Snapshot, subs []player.Track, l layout) {
	on := subtitlesOn(s)
	icon := iconSubtitlesOff
	if on {
		icon = iconSubtitlesOn
	}
	names := trackNames(subs)
	b := a.iconButton(c, "subtitles", icon, "Subtitles", "Subtitles (C)", l.button, 20).KeyShortcuts(0, ui.KeyC)
	b.PopoverMenuButton(func(m *ui.Menu) {
		if len(subs) > 0 {
			if m.Item("Off").ID("sid-off").Checked(!on).Chosen() && on {
				a.toggleSubtitles(s)
			}
			for i, tr := range subs {
				if m.Item(names[i]).ID(fmt.Sprintf("sid-%d", tr.ID)).Checked(on && tr.Selected).Chosen() {
					a.selectSubtitles(tr.ID)
				}
			}
			m.Separator()
		}
		if m.Item("Add Subtitles…").ID("sid-add").Chosen() {
			a.chooseSubtitles()
		}
	}, a.menuOptions(&a.subOpen))
}

// menuOptions opens a control bar menu above its button, lined up with its
// end, growing out of the button.
func (a *demoApp) menuOptions(open *bool) ui.PopoverMenuButtonOptions {
	return ui.PopoverMenuButtonOptions{
		At:   ui.AnchorTopEnd,
		Self: ui.AnchorBottomEnd,
		Open: open,
		Panel: func(panel ui.Element) {
			panel.AttachedOrigin().Transition(popoverTransition)
		},
	}
}

// hudButton is a round, borderless button for the control bar that tints
// under the pointer and gives a little when pressed.
func (a *demoApp) hudButton(c *ui.Context, key, label, tip string, size float32) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(size).MinWidth(size).Radius(size / 2).Center().Label(label)
	if tip != "" {
		b.Tooltip(tip)
	}
	pressed := b.Pressed()
	bg := transparent
	switch {
	case pressed:
		bg = hudPressed
	case b.Hovered():
		bg = hudHover
	}
	b.Background(b.AnimateColor("bg", bg, motionHover)).TextColor(hudText)
	b.AnimatePose(ui.Pose{Scale: pick(pressed, 0.9, 1)}, motionPress)
	return b
}

// iconButton is a hudButton showing an icon, which swaps in place when it
// changes (play ⇄ pause, volume levels, subtitles on and off).
func (a *demoApp) iconButton(c *ui.Context, key string, icon *ui.SVG, label, tip string, size, iconSize float32) ui.Element {
	b := a.hudButton(c, key, label, tip, size).Width(size)
	b.Children(func() {
		ui.Icon(c, icon).FontSize(iconSize).ContentTransition(contentSwap)
	})
	return b
}

func pick(cond bool, yes, no float32) float32 {
	if cond {
		return yes
	}
	return no
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
