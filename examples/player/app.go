package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
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
	seekHold = 1500 * time.Millisecond
	// volumeHold does the same for the volume.
	volumeHold = 800 * time.Millisecond
	// subtitleHold keeps the subtitles' on/off choice until playback
	// reports it, so pressing C twice quickly turns them off and on again.
	subtitleHold = 800 * time.Millisecond
	volumeStep   = 5
	skipStep     = 5 * time.Second
)

// mediaPlayer is what the example uses of *player.Player, so its interface
// can be exercised in headless tests without libmpv.
type mediaPlayer interface {
	Snapshot() player.Snapshot
	Open(target string, opts ...player.OpenOptions) error
	Pause() error
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
// thread: goroutines (file dialogs, the command queue) hand their results
// over with post.
type demoApp struct {
	win    *mygo.Window
	p      mediaPlayer
	canvas func(c *ui.Context) ui.Element
	cmds   *commandQueue // nil runs commands at once (tests)

	target  string // what is open (path or URL), "" when nothing is
	opening bool   // target's Open is queued or running
	openGen int    // counts opens, so a finished one only speaks for itself
	recent  string // local file to add to Open Recent once it plays

	lastActivity time.Time // last pointer movement or key press
	lastNow      time.Time // c.Now() from the last frame (keeps tests on Tester's clock)
	lastTouch    bool      // the last pointer input came from a finger
	chromeShown  bool      // whether the last frame showed the controls
	hudHot       bool      // pointer or keyboard focus in the control bar
	focusPlay    bool      // give the play button the focus next frame (Tab)

	speedOpen, audioOpen, subOpen, moreOpen bool // menus open

	scrubbing     bool
	scrubPos      float64 // seconds, while dragging or until playback catches up
	lastScrubSeek time.Time
	holdUntil     time.Time // show scrubPos instead of the reported position until then

	volDragging  bool
	volTarget    float64   // volume last set, until playback reports it
	volHoldUntil time.Time // show volTarget until then

	subWant      bool      // subtitles on or off as last asked for
	subHoldUntil time.Time // show subWant until then (after subPending)
	subPending   int       // subtitle commands queued or running

	// clickUndo records that the last click on the picture toggled playback
	// on clickUndoGen, so an immediately following double click can undo it.
	// The pointed-to bool is written and read only by the FIFO worker so an
	// undo runs only if the first click actually toggled playback.
	// hitClickDone records that the preceding click actually completed on
	// the picture (not a menu dismissal or canceled press).
	hitClickDone bool
	clickUndo    *bool
	clickUndoGen int
	onFullScreen func() // test hook; nil in production

	lastSubID int64 // subtitle track to bring back when subtitles are turned on
	lastSpeed float64
	speedDown bool // the speed last went down (its label rolls down)
	subPosSet int  // sub-pos last applied, 0 before any

	dark      *ui.Theme // theme of the controls over the picture
	darkPrefs ui.Preferences

	shownTitle string // window title last set
	shownFile  string // represented file last set
	pending    []ui.Toast
}

func newDemoApp(p mediaPlayer, canvas func(c *ui.Context) ui.Element) *demoApp {
	return &demoApp{p: p, canvas: canvas, lastSpeed: 1}
}

// commandQueue runs player commands one after another on a goroutine of
// its own: libmpv can hold a command for a long while (loading a file,
// attaching subtitles, a write while a file loads), which must never stall
// a frame. A command with a key replaces the last command waiting when it
// has the same key, so a dragged slider sends only its latest value; it
// never jumps ahead of other commands, which keeps their order (a seek
// stays with the file it was meant for).
type commandQueue struct {
	mu      sync.Mutex
	items   []command
	wake    chan struct{}
	stopped bool
}

type command struct {
	key string
	fn  func()
}

func newCommandQueue() *commandQueue {
	q := &commandQueue{wake: make(chan struct{}, 1)}
	go q.loop()
	return q
}

func (q *commandQueue) push(key string, fn func()) {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	if n := len(q.items); key != "" && n > 0 && q.items[n-1].key == key {
		q.items[n-1].fn = fn
	} else {
		q.items = append(q.items, command{key, fn})
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// stop drops the commands still waiting and takes no more, as the window
// closes.
func (q *commandQueue) stop() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.stopped, q.items = true, nil
	q.mu.Unlock()
}

func (q *commandQueue) loop() {
	for range q.wake {
		for {
			q.mu.Lock()
			if len(q.items) == 0 {
				q.mu.Unlock()
				break
			}
			c := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			c.fn()
		}
	}
}

// run sends a player command; onErr, unless nil, gets its error on the UI
// thread.
func (a *demoApp) run(key string, fn func() error, onErr func(error)) {
	if a.cmds == nil {
		if err := fn(); err != nil && onErr != nil {
			onErr(err)
		}
		return
	}
	a.cmds.push(key, func() {
		if err := fn(); err != nil && onErr != nil {
			a.post(func() { onErr(err) })
		}
	})
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

// now returns the current UI time: wall clock in a real window, or the last
// frame's clock in headless tests (Tester advances its own clock).
func (a *demoApp) now() time.Time {
	if a.win == nil && !a.lastNow.IsZero() {
		return a.lastNow
	}
	return time.Now()
}

// open plays target, with subtitle files to attach. UI thread only.
func (a *demoApp) open(target string, subs []string) {
	a.openGen++
	gen := a.openGen
	a.target, a.opening = target, true
	a.hitClickDone, a.clickUndo = false, nil
	a.scrubbing, a.holdUntil, a.volHoldUntil, a.subHoldUntil = false, time.Time{}, time.Time{}, time.Time{}
	a.lastSubID = 0
	a.lastActivity = a.now()
	a.recent = ""
	if isLocalFile(target) && a.win != nil {
		a.recent = target // recorded once it plays: a file that fails is no recent document
	}
	name := mediaName("", target)
	a.run("", func() error {
		err := a.p.Open(target, player.OpenOptions{Subtitles: subs})
		a.post(func() {
			if a.openGen == gen {
				a.opening = false
			}
		})
		return err
	}, func(err error) {
		a.fail(fmt.Sprintf("Couldn’t open “%s”", name), err)
	})
}

// openPaths opens what was dropped on the window, chosen in a dialog or
// opened from the desktop. The first video replaces what plays, with any
// subtitle files that came along; subtitle files alone are added to what
// plays. A file that is neither replaces nothing that plays, unless the
// user picked it on purpose (trusted: a dialog, "Open With").
func (a *demoApp) openPaths(paths []string, trusted bool) {
	video, other, subs := splitDropped(paths)
	has := a.hasMedia(a.p.Snapshot())
	switch {
	case video != "":
		a.open(video, subs)
		return
	case other != "" && (trusted || !has):
		a.open(other, subs)
		return
	case other != "":
		a.notify(ui.Toast{ID: "not-video", Title: fmt.Sprintf("Can’t open “%s”", filepath.Base(other)),
			Description: "Drop a video to play it, or subtitle files to add them."})
	}
	switch {
	case len(subs) > 0 && has:
		for _, s := range subs {
			a.addSubtitle(s)
		}
	case len(subs) > 0:
		a.notify(ui.Toast{ID: "need-video", Title: "Open a video first", Description: "Then drop subtitle files on it to add them."})
	}
}

func (a *demoApp) addSubtitle(path string) {
	name := filepath.Base(path)
	a.run("", func() error {
		return a.p.AddSubtitleFile(path, player.SubtitleSelect)
	}, func(err error) {
		a.fail(fmt.Sprintf("Couldn’t add “%s”", name), err)
	})
}

var videoFilter = mygo.FileFilter{Name: "Videos", Extensions: videoExtensions}

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
		a.post(func() { a.openPaths(paths[:1], true) })
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
	if a.target == "" {
		return false
	}
	if a.opening {
		return true // what was opened counts at once, as libmpv gets to it
	}
	switch s.State {
	case player.StateIdle, player.StateError:
		return false
	}
	return true
}

// seekable reports whether the media has a known length to seek in (live
// streams have none).
func seekable(s player.Snapshot) bool { return s.Seekable && s.Duration > 0 }

func (a *demoApp) togglePlay() {
	a.hitClickDone, a.clickUndo = false, nil
	a.run("", a.p.TogglePause, nil)
}

// pictureToggle plays or pauses immediately on a click on the picture and
// arms clickUndo so a double click can undo it. Ended media is left alone
// (both now and when the queued command runs): rewinding to 0:00 cannot be
// undone cleanly if the click turns out to be the first half of a double
// click leaving full screen (Replay and Space remain).
func (a *demoApp) pictureToggle() {
	s := a.p.Snapshot()
	if !a.opening && (s.EOF || s.State == player.StateEnded) {
		a.hitClickDone, a.clickUndo = true, nil
		return
	}
	var toggled bool
	a.hitClickDone, a.clickUndo, a.clickUndoGen = true, &toggled, a.openGen
	a.run("", func() error {
		s := a.p.Snapshot()
		if s.EOF || s.State == player.StateEnded {
			return nil
		}
		toggled = true
		return a.p.TogglePause()
	}, nil)
}

// undoPictureToggle undoes the playback toggle made by the first click of a
// double click, if that click actually toggled playback.
func (a *demoApp) undoPictureToggle() {
	toggled := a.clickUndo
	a.clickUndo = nil
	if toggled == nil {
		return
	}
	a.run("", func() error {
		if !*toggled {
			return nil
		}
		// If playback reached EOF during the brief play between the two
		// clicks, pause rather than replay from 0:00.
		if s := a.p.Snapshot(); s.EOF || s.State == player.StateEnded {
			return a.p.Pause()
		}
		return a.p.TogglePause()
	}, nil)
}

func (a *demoApp) skip(s player.Snapshot, d time.Duration) {
	if !seekable(s) {
		return
	}
	a.holdUntil = time.Time{}
	a.run("", func() error { return a.p.SeekRelative(d) }, nil)
}

func (a *demoApp) seek(pos float64) {
	a.run("seek", func() error { return a.p.Seek(secs(pos)) }, nil)
}

// volume is the volume to show: the one last set until playback reports
// it, so quick changes build on each other rather than on a stale value.
func (a *demoApp) volume(s player.Snapshot, now time.Time) float64 {
	if a.volDragging || now.Before(a.volHoldUntil) {
		return a.volTarget
	}
	return min(max(s.Volume, 0), 100)
}

// setVolume sets the volume: raising it from silence unmutes.
func (a *demoApp) setVolume(s player.Snapshot, v float64, now time.Time) {
	v = min(max(v, 0), 100)
	a.volTarget, a.volHoldUntil = v, now.Add(volumeHold)
	unmute := s.Muted && v > 0
	a.run("volume", func() error {
		if err := a.p.SetVolume(v); err != nil {
			return err
		}
		if unmute {
			return a.p.SetMuted(false)
		}
		return nil
	}, nil)
}

func (a *demoApp) changeVolume(s player.Snapshot, delta float64, now time.Time) {
	a.setVolume(s, a.volume(s, now)+delta, now)
}

// silent reports whether nothing is heard: muted, or at volume 0. The mute
// button then offers to unmute, and does.
func (a *demoApp) silent(s player.Snapshot, now time.Time) bool {
	return s.Muted || a.volume(s, now) <= 0
}

func (a *demoApp) toggleMute(s player.Snapshot, now time.Time) {
	if !s.Muted && a.volume(s, now) <= 0 {
		a.setVolume(s, 50, now) // silent at zero: unmuting is never silent
		return
	}
	// Decided by libmpv's own state as it runs, so quick presses each
	// flip it; it restores an audible volume when unmuting at zero.
	a.run("", a.p.ToggleMute, nil)
}

// subtitlesOn reports whether a subtitle track shows.
func subtitlesOn(s player.Snapshot) bool { return s.SubTrackID != 0 && s.SubVisible }

// subtitlesShown is subtitlesOn, or what the user last asked for until
// playback reports it.
func (a *demoApp) subtitlesShown(s player.Snapshot, now time.Time) bool {
	if a.subPending > 0 || now.Before(a.subHoldUntil) {
		return a.subWant
	}
	return subtitlesOn(s)
}

// runSubtitles sends a subtitle command; the choice it makes shows until
// it ran and playback had a moment to report it.
func (a *demoApp) runSubtitles(on bool, now time.Time, fn func() error) {
	a.subWant, a.subPending = on, a.subPending+1
	a.subHoldUntil = now.Add(subtitleHold)
	a.run("", func() error {
		err := fn()
		a.post(func() {
			a.subPending--
			a.subHoldUntil = a.now().Add(subtitleHold)
		})
		return err
	}, nil)
}

// toggleSubtitles turns subtitles off, or back on with the track shown last
// (else the first one).
func (a *demoApp) toggleSubtitles(s player.Snapshot, now time.Time) {
	if a.subtitlesShown(s, now) {
		if s.SubTrackID != 0 {
			a.lastSubID = s.SubTrackID
		}
		a.runSubtitles(false, now, a.p.DisableSubtitleTrack)
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
	a.selectSubtitles(id, now)
}

func (a *demoApp) selectSubtitles(id int64, now time.Time) {
	a.runSubtitles(true, now, func() error { return a.p.SelectSubtitleTrack(id) })
}

func (a *demoApp) selectAudio(id int64) {
	a.run("audio", func() error { return a.p.SelectAudioTrack(id) }, nil)
}

func (a *demoApp) setSpeed(rate float64) {
	a.run("speed", func() error { return a.p.SetSpeed(rate) }, nil)
}

func (a *demoApp) applySubPos(v int) {
	a.run("sub-pos", func() error { return a.p.SetSubtitlePos(v) }, nil)
}

func (a *demoApp) fullScreen() bool { return a.win != nil && a.win.IsFullScreen() }

func (a *demoApp) toggleFullScreen() {
	if a.win != nil {
		a.win.ToggleFullScreen()
	}
	if a.onFullScreen != nil {
		a.onFullScreen()
	}
}

func (a *demoApp) anyMenuOpen() bool { return a.speedOpen || a.audioOpen || a.subOpen || a.moreOpen }

// syncWindow names the window after what plays, and tells the system which
// file it shows (the macOS title bar's proxy icon).
func (a *demoApp) syncWindow(s player.Snapshot) {
	title, file := appTitle, ""
	if a.target != "" {
		title = a.name(s)
	}
	if isLocalFile(a.target) {
		file = a.target
	}
	if a.win == nil {
		return
	}
	if title != a.shownTitle {
		a.shownTitle = title
		a.win.SetTitle(title)
	}
	if file != a.shownFile {
		a.shownFile = file
		a.win.SetRepresentedFile(file)
	}
}

// name is what to call the media open now. While a new file is opening,
// the title libmpv reports is still the last file's, and is not used.
func (a *demoApp) name(s player.Snapshot) string {
	if a.opening {
		return mediaName("", a.target)
	}
	return mediaName(s.MediaTitle, a.target)
}

// shortcutLabel is how the platform writes a Cmd/Ctrl shortcut.
func shortcutLabel(key string) string {
	if runtime.GOOS == "darwin" {
		return "⌘" + key
	}
	return "Ctrl+" + key
}
