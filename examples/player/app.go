package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	player "github.com/arixbit/mygo-player"
)

const appTitle = "MyGo Player"

const (
	// hideChromeAfter is how long the controls stay after the last
	// movement or key press while a video plays.
	hideChromeAfter = 2500 * time.Millisecond
	// scrubSeekEvery throttles seeks while the seek bar is dragged; the
	// picture follows the drag at that rate and lands exactly on release.
	scrubSeekEvery = 60 * time.Millisecond
	// seekHold keeps the seek bar where the user put it until playback
	// reports the new position, so the thumb never jumps back.
	seekHold   = 1500 * time.Millisecond
	volumeStep = 5
	skipStep   = 5 * time.Second
)

// mediaPlayer is what the example uses of *player.Player, so its interface
// can be exercised in headless tests without libmpv.
type mediaPlayer interface {
	Snapshot() player.Snapshot
	Open(target string, opts ...player.OpenOptions) error
	TogglePause() error
	Seek(pos time.Duration) error
	SeekRelative(delta time.Duration) error
	SetVolume(v float64) error
	SetMuted(muted bool) error
	ToggleMute() error
	SetSpeed(rate float64) error
	SelectAudioTrack(id int64) error
	SelectSubtitleTrack(id int64) error
	DisableSubtitleTrack() error
	AddSubtitleFile(path string, flags ...player.SubtitleFlag) error
	SetSubtitlePos(pos int) error
}

// demoApp is the window's state. Every field is read and written on the UI
// thread: goroutines (file dialogs) hand their results over with post.
type demoApp struct {
	win    *mygo.Window
	p      mediaPlayer
	canvas func(c *ui.Context) ui.Element

	target string // what is open (path or URL), "" when nothing is

	lastActivity time.Time // last pointer movement or key press
	lastTouch    bool      // the last pointer input came from a finger
	chromeShown  bool      // whether the last frame showed the controls
	hudHot       bool      // pointer or keyboard focus in the control bar

	speedOpen, audioOpen, subOpen bool // menus open

	scrubbing     bool
	scrubPos      float64 // seconds, while dragging or until playback catches up
	lastScrubSeek time.Time
	holdUntil     time.Time // show scrubPos instead of the reported position until then

	volDragging bool
	volDrag     float64

	lastSubID int64 // subtitle track to bring back when subtitles are turned on
	lastSpeed float64
	speedDown bool // the speed last went down (its label rolls down)
	subPosSet int  // sub-pos last applied, 0 before any
	// subPos hands sub-pos changes to a goroutine: libmpv can hold a
	// property write for a long while (as when a file is loading), which
	// must not stall the frame. Nil applies them at once (tests).
	subPos chan int

	dark      *ui.Theme // theme of the controls over the picture
	darkPrefs ui.Preferences

	shownTitle string // window title last set
	pending    []ui.Toast
}

func newDemoApp(p mediaPlayer, canvas func(c *ui.Context) ui.Element) *demoApp {
	return &demoApp{p: p, canvas: canvas, lastSpeed: 1}
}

// startSubPosWorker applies sub-pos changes off the UI thread, the latest
// one winning.
func (a *demoApp) startSubPosWorker() {
	a.subPos = make(chan int, 1)
	go func() {
		for v := range a.subPos {
			_ = a.p.SetSubtitlePos(v)
		}
	}()
}

func (a *demoApp) applySubPos(v int) {
	if a.subPos == nil {
		_ = a.p.SetSubtitlePos(v)
		return
	}
	select {
	case <-a.subPos: // drop a value not applied yet
	default:
	}
	a.subPos <- v
}

// post runs fn on the UI thread and redraws, from any goroutine.
func (a *demoApp) post(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *demoApp) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// notify shows a toast at the next frame. UI thread only.
func (a *demoApp) notify(t ui.Toast) {
	a.pending = append(a.pending, t)
	a.invalidate()
}

func (a *demoApp) fail(title string, err error) {
	a.notify(ui.Toast{ID: title, Title: title, Description: friendlyError(err), Type: "error", Timeout: 6 * time.Second})
}

// poke records user activity, which brings the controls back.
func (a *demoApp) poke(now time.Time) {
	a.lastActivity = now
	if !a.chromeShown {
		a.invalidate()
	}
}

// open plays target, with subtitle files to attach. UI thread only.
func (a *demoApp) open(target string, subs []string) {
	a.target = target
	a.scrubbing, a.holdUntil = false, time.Time{}
	a.lastSubID = 0
	a.lastActivity = time.Now()
	if err := a.p.Open(target, player.OpenOptions{Subtitles: subs}); err != nil {
		a.fail(fmt.Sprintf("Couldn’t open “%s”", mediaName("", target)), err)
		return
	}
	if isLocalFile(target) && a.win != nil {
		_ = mygo.App.AddRecentDocument(target)
	}
}

// openPaths opens what was dropped on the window or opened from the
// desktop: the first video replaces what plays, with any subtitle files
// dropped along with it; subtitle files alone are added to what plays.
func (a *demoApp) openPaths(paths []string) {
	video, subs := splitDropped(paths)
	switch {
	case video != "":
		a.open(video, subs)
	case len(subs) > 0 && a.hasMedia(a.p.Snapshot()):
		for _, s := range subs {
			a.addSubtitle(s)
		}
	case len(subs) > 0:
		a.notify(ui.Toast{ID: "need-video", Title: "Open a video first", Description: "Then drop subtitle files on it to add them."})
	}
}

func (a *demoApp) addSubtitle(path string) {
	if err := a.p.AddSubtitleFile(path, player.SubtitleSelect); err != nil {
		a.fail(fmt.Sprintf("Couldn’t add “%s”", filepath.Base(path)), err)
	}
}

var videoFilter = mygo.FileFilter{Name: "Videos", Extensions: []string{"mkv", "mp4", "m4v", "mov", "webm", "avi", "ts", "m2ts", "mts", "flv", "wmv", "mpg", "mpeg", "ogv", "3gp"}}

func (a *demoApp) chooseVideo() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   "Choose a Video",
			Filters: []mygo.FileFilter{videoFilter, {Name: "All Files", Extensions: []string{"*"}}},
		})
		if err != nil || len(paths) == 0 {
			return // canceled
		}
		a.post(func() { a.openPaths(paths[:1]) })
	}()
}

func (a *demoApp) chooseSubtitles() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   "Add Subtitles",
			Filters: []mygo.FileFilter{{Name: "Subtitles", Extensions: []string{"srt", "ass", "ssa", "vtt", "sub", "sup"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.addSubtitle(paths[0]) })
	}()
}

// hasMedia reports whether something is open (loading, playing, paused or
// ended), as opposed to idle or failed.
func (a *demoApp) hasMedia(s player.Snapshot) bool {
	switch s.State {
	case player.StateIdle, player.StateError:
		return false
	}
	return a.target != ""
}

func (a *demoApp) togglePlay() { _ = a.p.TogglePause() }

func (a *demoApp) skip(d time.Duration) {
	a.holdUntil = time.Time{}
	_ = a.p.SeekRelative(d)
}

// changeVolume nudges the volume; turning it up unmutes.
func (a *demoApp) changeVolume(s player.Snapshot, delta float64) {
	v := min(max(s.Volume+delta, 0), 100)
	_ = a.p.SetVolume(v)
	if s.Muted && delta > 0 {
		_ = a.p.SetMuted(false)
	}
}

// setVolume sets the volume from the slider: dragging up from silence
// unmutes, dragging to zero is the same as muting.
func (a *demoApp) setVolume(s player.Snapshot, v float64) {
	_ = a.p.SetVolume(v)
	if s.Muted && v > 0 {
		_ = a.p.SetMuted(false)
	}
}

func (a *demoApp) toggleMute() { _ = a.p.ToggleMute() }

// subtitlesOn reports whether a subtitle track shows.
func subtitlesOn(s player.Snapshot) bool { return s.SubTrackID != 0 && s.SubVisible }

// toggleSubtitles turns subtitles off, or back on with the track shown last
// (else the first one).
func (a *demoApp) toggleSubtitles(s player.Snapshot) {
	if subtitlesOn(s) {
		a.lastSubID = s.SubTrackID
		_ = a.p.DisableSubtitleTrack()
		return
	}
	subs := filterByType(s.Tracks, player.TrackSub)
	if len(subs) == 0 {
		return
	}
	id := subs[0].ID
	for _, tr := range subs {
		if tr.ID == a.lastSubID {
			id = tr.ID
		}
	}
	_ = a.p.SelectSubtitleTrack(id)
}

func (a *demoApp) setSpeed(rate float64) { _ = a.p.SetSpeed(rate) }

func (a *demoApp) fullScreen() bool { return a.win != nil && a.win.IsFullScreen() }

func (a *demoApp) toggleFullScreen() {
	if a.win != nil {
		a.win.ToggleFullScreen()
	}
}

func (a *demoApp) anyMenuOpen() bool { return a.speedOpen || a.audioOpen || a.subOpen }

// syncWindow names the window after what plays, and tells the system which
// file it shows (the macOS title bar's proxy icon).
func (a *demoApp) syncWindow(s player.Snapshot) {
	title := appTitle
	if a.target != "" {
		title = mediaName(s.MediaTitle, a.target)
	}
	if title == a.shownTitle || a.win == nil {
		a.shownTitle = title
		return
	}
	a.shownTitle = title
	a.win.SetTitle(title)
	if isLocalFile(a.target) {
		a.win.SetRepresentedFile(a.target)
	} else {
		a.win.SetRepresentedFile("")
	}
}

// shortcutLabel is how the platform writes a Cmd/Ctrl shortcut.
func shortcutLabel(key string) string {
	if runtime.GOOS == "darwin" {
		return "⌘" + key
	}
	return "Ctrl+" + key
}
