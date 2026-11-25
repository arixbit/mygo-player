package main

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	player "github.com/arixbit/mygo-player"
)

// fakePlayer stands in for *player.Player so the example's interface can be
// exercised headlessly, without libmpv.
type fakePlayer struct {
	snap     player.Snapshot
	opened   []string
	openSubs [][]string
	added    []string
	seeks    []time.Duration
	subPos   int
	openErr  error
	addErr   error
	nAudio   int
}

func newFakePlayer() *fakePlayer {
	return &fakePlayer{nAudio: 2, snap: player.Snapshot{State: player.StateIdle, Volume: 80, Speed: 1, SubVisible: true, SubScale: 1, SubPos: 100}}
}

func (f *fakePlayer) Snapshot() player.Snapshot {
	s := f.snap
	s.Tracks = append([]player.Track(nil), f.snap.Tracks...)
	return s
}

func (f *fakePlayer) Open(target string, opts ...player.OpenOptions) error {
	f.opened = append(f.opened, target)
	var subs []string
	if len(opts) > 0 {
		subs = opts[0].Subtitles
	}
	f.openSubs = append(f.openSubs, subs)
	if f.openErr != nil {
		f.snap.State, f.snap.Err = player.StateError, f.openErr
		return nil // libmpv reports load failures asynchronously
	}
	f.snap.State, f.snap.Err, f.snap.Paused, f.snap.EOF = player.StatePlaying, nil, false, false
	f.snap.Position, f.snap.Duration, f.snap.Seekable = 10*time.Second, 90*time.Second, true
	f.snap.VideoWidth, f.snap.VideoHeight = 1280, 720
	f.snap.MediaTitle = "Big Test Movie"
	f.snap.Tracks = []player.Track{{ID: 1, Type: player.TrackVideo, Selected: true}}
	for i := 1; i <= f.nAudio; i++ {
		f.snap.Tracks = append(f.snap.Tracks, player.Track{ID: int64(i), Type: player.TrackAudio, Lang: []string{"eng", "jpn", "fre"}[i-1], Selected: i == 1})
	}
	f.snap.Tracks = append(f.snap.Tracks, player.Track{ID: 1, Type: player.TrackSub, Lang: "eng", Selected: true})
	f.snap.AudioTrackID, f.snap.SubTrackID = 1, 1
	return nil
}

func (f *fakePlayer) Pause() error {
	f.snap.Paused = true
	if !f.snap.EOF {
		f.snap.State = player.StatePaused
	}
	return nil
}

func (f *fakePlayer) TogglePause() error {
	if f.snap.EOF || f.snap.State == player.StateEnded {
		f.snap.EOF, f.snap.Position, f.snap.Paused, f.snap.State = false, 0, false, player.StatePlaying
		return nil
	}
	f.snap.Paused = !f.snap.Paused
	f.snap.State = map[bool]player.State{true: player.StatePaused, false: player.StatePlaying}[f.snap.Paused]
	return nil
}

func (f *fakePlayer) Seek(pos time.Duration) error {
	f.seeks = append(f.seeks, pos)
	return nil // the position is reported later, as libmpv does
}

func (f *fakePlayer) SeekRelative(d time.Duration) error { f.snap.Position += d; return nil }
func (f *fakePlayer) SetVolume(v float64) error          { f.snap.Volume = v; return nil }
func (f *fakePlayer) SetMuted(m bool) error              { f.snap.Muted = m; return nil }
func (f *fakePlayer) SetSpeed(r float64) error           { f.snap.Speed = r; return nil }
func (f *fakePlayer) SetSubtitlePos(p int) error         { f.subPos = p; return nil }

func (f *fakePlayer) ToggleMute() error {
	if f.snap.Muted && f.snap.Volume <= 0 {
		f.snap.Volume = 50
	}
	f.snap.Muted = !f.snap.Muted
	return nil
}

func (f *fakePlayer) selectTrack(kind player.TrackType, id int64) {
	for i := range f.snap.Tracks {
		if f.snap.Tracks[i].Type == kind {
			f.snap.Tracks[i].Selected = f.snap.Tracks[i].ID == id
		}
	}
}

func (f *fakePlayer) SelectAudioTrack(id int64) error {
	f.snap.AudioTrackID = id
	f.selectTrack(player.TrackAudio, id)
	return nil
}

func (f *fakePlayer) SelectSubtitleTrack(id int64) error {
	f.snap.SubTrackID, f.snap.SubVisible = id, true
	f.selectTrack(player.TrackSub, id)
	return nil
}

func (f *fakePlayer) DisableSubtitleTrack() error {
	f.snap.SubTrackID = 0
	f.selectTrack(player.TrackSub, 0)
	return nil
}

func (f *fakePlayer) AddSubtitleFile(path string, _ ...player.SubtitleFlag) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, path)
	id := int64(len(filterByType(f.snap.Tracks, player.TrackSub)) + 1)
	f.snap.Tracks = append(f.snap.Tracks, player.Track{ID: id, Type: player.TrackSub, External: true, ExternalFilename: path})
	return f.SelectSubtitleTrack(id)
}

func newTestApp(t *testing.T, w, h int) (*demoApp, *fakePlayer, *ui.Tester) {
	t.Helper()
	f := newFakePlayer()
	a := newDemoApp(f, func(c *ui.Context) ui.Element { return ui.Box(c).Background(stageBlack) })
	tt := ui.NewTester(a.view, w, h)
	tt.Frame()
	return a, f, tt
}

// visible reports whether assistive technology (and so the user) can reach
// a control named name.
func visible(tt *ui.Tester, name string) bool {
	_, ok := tt.Access(name)
	return ok
}

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("PLAYER_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	fh, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if err := png.Encode(fh, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestIdleShowsOnlyTheWelcome(t *testing.T) {
	a, _, tt := newTestApp(t, 960, 600)
	if !tt.HasText("Drop a Video Here") || !visible(tt, "Choose Video…") {
		t.Fatalf("welcome missing: %q", tt.Texts())
	}
	for _, name := range []string{"Pause", "Play", "Playback position", "Subtitles", "Full Screen"} {
		if visible(tt, name) {
			t.Errorf("%q shows with nothing open", name)
		}
	}
	if a.chromeShown {
		t.Error("controls show with nothing open")
	}
	snapshot(t, tt, "idle-light")
	tt.SetDark(true)
	tt.Advance(time.Second)
	snapshot(t, tt, "idle-dark")
}

func TestControlsHideWhilePlayingAndComeBack(t *testing.T) {
	a, _, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	if !visible(tt, "Pause") || !a.chromeShown {
		t.Fatal("controls should show right after opening")
	}
	if tt.HasText("Drop a Video Here") {
		tt.Advance(time.Second) // let the welcome card finish leaving
	}
	snapshot(t, tt, "playing")
	tt.Move(300, 200)
	tt.Advance(hideChromeAfter + time.Second)
	if a.chromeShown || visible(tt, "Pause") {
		t.Fatal("controls should hide after a while of playing")
	}
	t.Logf("chrome %v hover %v cursor %v none %v", a.chromeShown, a.hudHot, tt.Cursor(), ui.CursorNone)
	if tt.Cursor() != ui.CursorNone {
		t.Errorf("cursor = %v, want hidden over the playing video", tt.Cursor())
	}
	snapshot(t, tt, "playing-hidden")
	tt.Move(400, 300)
	tt.Move(410, 300)
	if !a.chromeShown || !visible(tt, "Pause") {
		t.Fatal("moving the pointer should bring the controls back")
	}
}

func TestControlsStayWhilePaused(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	if !f.snap.Paused {
		t.Fatal("Space should pause")
	}
	tt.Advance(10 * time.Second)
	if !visible(tt, "Play") {
		t.Fatal("controls should stay while paused")
	}
	snapshot(t, tt, "paused")
}

func TestAudioMenuOnlyWithAChoice(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	f.nAudio = 1
	a.open("/movies/one.mkv", nil)
	tt.Frame()
	if visible(tt, "Audio Track") {
		t.Error("audio track menu shows for a single track")
	}
	f.nAudio = 2
	a.open("/movies/two.mkv", nil)
	tt.Frame()
	if !visible(tt, "Audio Track") {
		t.Error("audio track menu missing for two tracks")
	}
}

func TestShortcuts(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()

	tt.Key(0, ui.KeyRight)
	if f.snap.Position != 15*time.Second {
		t.Errorf("Right: position %v, want 15s", f.snap.Position)
	}
	tt.Key(0, ui.KeyDown)
	if f.snap.Volume != 75 {
		t.Errorf("Down: volume %v, want 75", f.snap.Volume)
	}
	tt.Key(0, ui.KeyM)
	if !f.snap.Muted {
		t.Error("M should mute")
	}
	tt.Key(0, ui.KeyUp)
	if f.snap.Muted || f.snap.Volume != 80 {
		t.Errorf("Up should unmute and raise the volume: muted %v volume %v", f.snap.Muted, f.snap.Volume)
	}
	tt.Key(0, ui.KeyC)
	if f.snap.SubTrackID != 0 {
		t.Error("C should turn subtitles off")
	}
	tt.Key(0, ui.KeyC)
	if f.snap.SubTrackID != 1 {
		t.Error("C should bring the subtitles back")
	}
}

func TestClickingAButtonKeepsSpaceForPlayback(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	if err := tt.Click("Mute"); err != nil {
		t.Fatal(err)
	}
	if !f.snap.Muted {
		t.Fatal("Mute did not mute")
	}
	tt.Key(0, ui.KeySpace)
	if !f.snap.Muted || !f.snap.Paused {
		t.Errorf("Space after clicking Mute: muted %v paused %v; want it to pause, not unmute", f.snap.Muted, f.snap.Paused)
	}
}

func TestUnmuteAtZeroIsAudible(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	f.snap.Volume, f.snap.Muted = 0, true
	tt.Frame()
	if err := tt.Click("Unmute"); err != nil {
		t.Fatal(err)
	}
	if f.snap.Muted || f.snap.Volume <= 0 {
		t.Errorf("unmute left it silent: muted %v volume %v", f.snap.Muted, f.snap.Volume)
	}
}

func TestDroppingFiles(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)

	tt.DropFiles(480, 300, "/movies/movie.en.srt")
	if len(f.opened) != 0 || len(f.added) != 0 {
		t.Fatal("a subtitle file alone should not open anything when idle")
	}
	tt.Frame()
	if !tt.HasText("Open a video first") {
		t.Errorf("no hint after dropping subtitles on an empty window: %q", tt.Texts())
	}

	tt.DropFiles(480, 300, "/movies/movie.en.srt", "/movies/movie.mkv")
	if len(f.opened) != 1 || f.opened[0] != "/movies/movie.mkv" || len(f.openSubs[0]) != 1 {
		t.Fatalf("video and subtitles dropped together: opened %v with %v", f.opened, f.openSubs)
	}
	tt.Frame()

	tt.DropFiles(480, 300, "/movies/extra.ass")
	if len(f.opened) != 1 || len(f.added) != 1 {
		t.Fatalf("subtitles dropped on a playing video should be added: opened %v added %v", f.opened, f.added)
	}
	if a.target != "/movies/movie.mkv" {
		t.Errorf("target = %q", a.target)
	}
}

func TestErrorsDoNotBlock(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	f.addErr = errors.New("unsupported format")
	tt.DropFiles(480, 300, "/movies/broken.sub")
	tt.Frame()
	if !tt.HasText("Couldn’t add “broken.sub”") || !visible(tt, "Pause") {
		t.Errorf("a subtitle error should be a toast over the still-usable player: %q", tt.Texts())
	}
	tt.Advance(10 * time.Second)
	if tt.HasText("Couldn’t add “broken.sub”") {
		t.Error("the toast should go away on its own")
	}

	f.openErr = errors.New("loading failed")
	a.open("/movies/bad.mkv", nil)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText("Can’t Play This Video") || !visible(tt, "Choose Another Video…") {
		t.Errorf("a failed file should explain itself and offer a way on: %q", tt.Texts())
	}
	snapshot(t, tt, "error")
}

func TestSeekBarHoldsWhereItWasLetGo(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	info, ok := tt.Access("Playback position")
	if !ok {
		t.Fatal("no seek bar")
	}
	b := info.Bounds
	y := b.Y + b.H/2
	tt.Press(b.X+6, y)
	for i := 1; i <= 10; i++ {
		tt.Move(b.X+6+float32(i)*(b.W-12)*0.075, y) // drag to 75%
		tt.Advance(16 * time.Millisecond)
	}
	tt.Release(b.X+6+(b.W-12)*0.75, y)
	tt.Frame()
	if len(f.seeks) == 0 {
		t.Fatal("dragging the seek bar did not seek")
	}
	last := f.seeks[len(f.seeks)-1]
	if d := last - time.Duration(0.75*90*float64(time.Second)); d < -time.Second || d > time.Second {
		t.Errorf("last seek %v, want about 67.5s", last)
	}
	if len(f.seeks) > 8 {
		t.Errorf("%d seeks for one drag; dragging should be throttled", len(f.seeks))
	}
	// libmpv has not reported the new position yet: the bar stays put.
	if !tt.HasText("1:07") {
		t.Errorf("elapsed time should show where the bar was let go: %q", tt.Texts())
	}
	f.snap.Position = last
	tt.Advance(seekHold + 100*time.Millisecond)
	if !a.holdUntil.IsZero() || a.scrubbing {
		t.Error("hold should end once playback catches up")
	}
}

func TestNarrowWindowDropsWhatDoesNotFit(t *testing.T) {
	for _, w := range []int{320, 360, 420, 520, 640, 1440} {
		a, _, tt := newTestApp(t, w, 640)
		a.open("/movies/movie.mkv", nil)
		tt.Frame()
		tt.Advance(time.Second)
		for _, n := range tt.AccessAll() {
			if n.Bounds.W == 0 || strings.HasPrefix(n.Name, "Playback controls") {
				continue
			}
			if n.Bounds.X < 0 || n.Bounds.X+n.Bounds.W > float32(w)+0.5 {
				t.Errorf("width %d: %q overflows: %+v", w, n.Name, n.Bounds)
			}
		}
		if w < 400 && visible(tt, "Back 5 Seconds") {
			t.Errorf("width %d: skip buttons should give way", w)
		}
		if w >= 640 && !visible(tt, "Volume") {
			t.Errorf("width %d: volume slider missing", w)
		}
		snapshot(t, tt, "width-"+itoa(w))
	}
}

func TestReduceMotionStillWorks(t *testing.T) {
	f := newFakePlayer()
	a := newDemoApp(f, func(c *ui.Context) ui.Element { return ui.Box(c).Background(stageBlack) })
	tt := ui.NewTesterWith(a.view, 960, 600, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, ReduceTransparency: true, TextScale: 1}})
	tt.Frame()
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	if !visible(tt, "Pause") {
		t.Fatal("controls missing with reduced motion")
	}
	tt.Advance(hideChromeAfter + time.Second)
	if visible(tt, "Pause") {
		t.Fatal("controls should still hide with reduced motion")
	}
}

func TestSubtitlesMakeRoomForTheControls(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	tt.Advance(time.Second)
	if f.subPos >= 100 || f.subPos < 70 {
		t.Errorf("sub-pos %d with the controls showing, want lifted above them", f.subPos)
	}
	tt.Advance(hideChromeAfter + time.Second)
	if f.subPos != 100 {
		t.Errorf("sub-pos %d with the controls hidden, want 100", f.subPos)
	}
}

func TestFormatting(t *testing.T) {
	for _, tc := range []struct {
		got, want string
	}{
		{formatClock(65*time.Second, false), "1:05"},
		{formatClock(65*time.Second, true), "0:01:05"},
		{formatClock(3725*time.Second, false), "1:02:05"},
		{formatRemaining(10*time.Second, 90*time.Second), "-1:20"},
		{formatRemaining(89500*time.Millisecond, 90*time.Second), "-0:01"},
		{formatRemaining(90*time.Second, 90*time.Second), "-0:00"},
		{speedLabel(1), "1×"},
		{speedLabel(1.25), "1.25×"},
		{mediaName("", "https://x.test/a/ep1.mkv?token=1"), "ep1.mkv"},
		{mediaName("Title", "/a/b.mkv"), "Title"},
		{mediaName("movie.mkv?token=SECRET", "https://x.test/movie.mkv?token=SECRET"), "movie.mkv"},
		{mediaName("Real Title", "https://x.test/movie.mkv?token=SECRET"), "Real Title"},
		{mediaName("", "/a/what?.mkv"), "what?.mkv"},
		{mediaName("abc", "https://x.test/movie.mkv?token=EXAMPLE%2Fabc"), "movie.mkv"},
		{mediaName("cap tions.mkv?token=X", "https://x.test/cap%20tions.mkv?token=X"), "cap tions.mkv"},
		{mediaName("watch.mkv?token=EXAMPLE", "https://x.test/watch"), "watch.mkv"},
		{mediaName("What? A Film", "https://x.test/watch"), "What? A Film"},
		{friendlyError(errors.New("mpv playback: unrecognized file format (-17)")), "Unrecognized file format."},
		{friendlyError(errors.New("player: closed")), "Closed."},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	names := trackNames([]player.Track{
		{ID: 1, Lang: "eng", Codec: "aac"},
		{ID: 2, Lang: "eng", Codec: "ac3"},
		{ID: 3, Lang: "jpn"},
	})
	if strings.Join(names, "|") != "English · AAC|English · AC3|Japanese" {
		t.Errorf("trackNames = %q", names)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestPictureClickPausesImmediatelyAndDoubleClickUndoes(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	fsCount := 0
	a.onFullScreen = func() { fsCount++ }
	a.open("/movies/movie.mkv", nil)
	tt.Frame()

	// Single click pauses immediately (zero wait) without toggling full screen.
	tt.ClickAt(480, 200)
	if !f.snap.Paused || fsCount != 0 {
		t.Fatalf("single click on playing video: paused=%v fs=%d, want true/0", f.snap.Paused, fsCount)
	}
	tt.Advance(time.Second) // past double-click interval
	// Another single click resumes immediately.
	tt.ClickAt(480, 200)
	if f.snap.Paused || fsCount != 0 {
		t.Fatalf("single click on paused video: paused=%v fs=%d, want false/0", f.snap.Paused, fsCount)
	}

	// Fast double click on playing video: pauses on click 1, undoes on click 2 + toggles full screen.
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	if !f.snap.Paused {
		t.Fatal("first click of double click should pause immediately")
	}
	tt.ClickAt(480, 200)
	if f.snap.Paused || fsCount != 1 {
		t.Fatalf("fast double click on playing video: paused=%v fs=%d, want false/1", f.snap.Paused, fsCount)
	}

	// Slower double click (300ms apart, still within OS double-click interval) on playing video.
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	if !f.snap.Paused {
		t.Fatal("first click of 300ms double click should pause immediately")
	}
	tt.Advance(300 * time.Millisecond)
	tt.ClickAt(480, 200)
	if f.snap.Paused || fsCount != 2 {
		t.Fatalf("300ms double click on playing video: paused=%v fs=%d, want false/2", f.snap.Paused, fsCount)
	}

	// Double click on paused video: resumes on click 1, undoes back to paused on click 2 + toggles full screen.
	tt.Advance(time.Second)
	tt.ClickAt(480, 200) // now paused
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	if f.snap.Paused {
		t.Fatal("first click on paused video should play immediately")
	}
	tt.Advance(150 * time.Millisecond)
	tt.ClickAt(480, 200)
	if !f.snap.Paused || fsCount != 3 {
		t.Fatalf("double click on paused video: paused=%v fs=%d, want true/3", f.snap.Paused, fsCount)
	}

	// Double click at EOF: toggles full screen and leaves the movie ended at 90s.
	f.snap.EOF, f.snap.State, f.snap.Position = true, player.StateEnded, 90*time.Second
	tt.Frame()
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	tt.Advance(150 * time.Millisecond)
	tt.ClickAt(480, 200)
	if !f.snap.EOF || f.snap.Position != 90*time.Second || fsCount != 4 {
		t.Fatalf("double click at EOF: eof=%v pos=%v fs=%d, want true/90s/4", f.snap.EOF, f.snap.Position, fsCount)
	}

	// Two quick clicks at distant points are independent clicks (pause, then play), not a double click.
	a.open("/movies/movie2.mkv", nil)
	tt.Frame()
	tt.Advance(time.Second)
	tt.ClickAt(200, 200)
	tt.Advance(50 * time.Millisecond)
	tt.ClickAt(700, 200)
	if f.snap.Paused || fsCount != 4 {
		t.Fatalf("distant quick clicks: paused=%v fs=%d, want false/4", f.snap.Paused, fsCount)
	}

	// Canceled second press must not undo click 1, toggle full screen, or poison the next click.
	tt.Advance(time.Second)
	tt.ClickAt(480, 200) // paused
	tt.Advance(100 * time.Millisecond)
	tt.Press(480, 200)
	tt.CancelPress()
	if !f.snap.Paused || fsCount != 4 {
		t.Fatalf("canceled second press: paused=%v fs=%d, want true/4", f.snap.Paused, fsCount)
	}
	tt.Advance(100 * time.Millisecond)
	tt.ClickAt(480, 200) // genuine click -> plays
	if f.snap.Paused || fsCount != 4 {
		t.Fatalf("click after canceled press: paused=%v fs=%d, want false/4", f.snap.Paused, fsCount)
	}

	// Opening a new movie after click 1 clears undo state so click 2 doesn't corrupt the new movie.
	tt.Advance(time.Second)
	tt.ClickAt(480, 200) // pauses movie2
	tt.Advance(80 * time.Millisecond)
	a.open("/movies/movie3.mkv", nil)
	tt.Frame()
	tt.Advance(80 * time.Millisecond)
	tt.ClickAt(480, 200) // fresh single click on movie3 -> pauses movie3, no fullscreen
	if !f.snap.Paused || fsCount != 4 {
		t.Fatalf("click on newly opened movie: paused=%v fs=%d, want true/4", f.snap.Paused, fsCount)
	}

	// Clicking the picture once, opening a menu, dismissing it by clicking the
	// picture, then quickly clicking the same spot must not arm fullscreen from
	// the stale pre-menu click.
	a.open("/movies/movie4.mkv", nil)
	tt.Frame()
	tt.ClickAt(480, 200) // pauses and sets hitClickDone=true
	if err := tt.Click("Subtitles"); err != nil {
		t.Fatal(err)
	}
	if !a.subOpen {
		t.Fatal("subtitles menu did not open")
	}
	tt.ClickAt(480, 200) // dismiss menu only (must clear stale hitClickDone too)
	if !f.snap.Paused || a.subOpen || fsCount != 4 {
		t.Fatalf("click to dismiss menu: paused=%v subOpen=%v fs=%d, want true/false/4", f.snap.Paused, a.subOpen, fsCount)
	}
	tt.Advance(150 * time.Millisecond)
	tt.ClickAt(480, 200) // fresh single click -> resumes, must NOT toggle fullscreen
	if f.snap.Paused || fsCount != 4 {
		t.Fatalf("click right after menu dismiss: paused=%v fs=%d, want false/4", f.snap.Paused, fsCount)
	}

	// Touch tap toggles chrome without pausing playback.
	a.open("/movies/movie5.mkv", nil)
	tt.Frame()
	tt.UsePointer(ui.PointerTouch, 0.5)
	tt.ClickAt(480, 200)
	if f.snap.Paused || a.chromeShown {
		t.Fatalf("touch tap while controls shown: paused=%v chrome=%v, want false/false", f.snap.Paused, a.chromeShown)
	}
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	if f.snap.Paused || !a.chromeShown {
		t.Fatalf("touch tap while controls hidden: paused=%v chrome=%v, want false/true", f.snap.Paused, a.chromeShown)
	}
	tt.UsePointer(ui.PointerMouse, 0.5)

	// Double click while the worker queue is held still preserves playing state after draining.
	a.open("/movies/movie6.mkv", nil)
	tt.Frame()
	q, release, drain := heldQueue()
	a.cmds = q
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	tt.Advance(120 * time.Millisecond)
	tt.ClickAt(480, 200)
	release()
	drain()
	if f.snap.Paused || fsCount != 5 {
		t.Fatalf("double click with busy worker: paused=%v fs=%d, want false/5", f.snap.Paused, fsCount)
	}

	// If playback reaches EOF while a queued picture double click is waiting on
	// the worker, neither click may rewind the movie to 0:00.
	q2, release2, drain2 := heldQueue()
	a.cmds = q2
	tt.Advance(time.Second)
	tt.ClickAt(480, 200)
	tt.Advance(120 * time.Millisecond)
	tt.ClickAt(480, 200)
	// Media reaches EOF before the queued picture clicks run.
	f.snap.EOF, f.snap.State, f.snap.Position = true, player.StateEnded, 90*time.Second
	release2()
	drain2()
	if !f.snap.EOF || f.snap.Position != 90*time.Second || fsCount != 6 {
		t.Fatalf("queued double click hitting EOF: eof=%v pos=%v fs=%d, want true/90s/6", f.snap.EOF, f.snap.Position, fsCount)
	}
}

// heldQueue returns a command queue whose worker waits until release is
// called, as libmpv busy loading a file holds it, and drain, which waits
// for every command to have run.
func heldQueue() (q *commandQueue, release, drain func()) {
	q = newCommandQueue()
	gate := make(chan struct{})
	q.push("", func() { <-gate })
	release = func() { close(gate) }
	drain = func() {
		done := make(chan struct{})
		q.push("", func() { close(done) })
		<-done
	}
	return q, release, drain
}

func TestCommandQueueCoalescesWithoutReordering(t *testing.T) {
	q, release, drain := heldQueue()
	var got []string
	q.push("seek", func() { got = append(got, "seek A") })
	q.push("", func() { got = append(got, "open B") })
	q.push("seek", func() { got = append(got, "seek B1") })
	q.push("seek", func() { got = append(got, "seek B2") })
	release()
	drain()
	if strings.Join(got, ", ") != "seek A, open B, seek B2" {
		t.Errorf("ran %q; want seeks kept with their file, the latest of a run only", got)
	}
	q.stop()
	ran := false
	q.push("", func() { ran = true })
	time.Sleep(20 * time.Millisecond)
	if ran {
		t.Error("a stopped queue ran a command")
	}
}

func TestQuickTogglesWhilePlayerIsBusy(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	q, release, drain := heldQueue()
	a.cmds = q
	tt.Key(0, ui.KeyC)
	tt.Key(0, ui.KeyC)
	tt.Key(0, ui.KeyM)
	tt.Key(0, ui.KeyM)
	release()
	drain()
	if f.snap.SubTrackID == 0 || f.snap.Muted {
		t.Errorf("two quick presses each should flip: subtitles track %d, muted %v", f.snap.SubTrackID, f.snap.Muted)
	}
}

func TestDropsWhileAVideoIsOpening(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	q, release, drain := heldQueue()
	a.cmds = q
	tt.DropFiles(480, 300, "/movies/a.mkv")
	tt.DropFiles(480, 300, "/movies/a.srt")
	tt.DropFiles(480, 300, "/docs/readme.txt")
	tt.Frame()
	if tt.HasText("Open a video first") || !tt.HasText("Can’t open “readme.txt”") {
		t.Errorf("drops during an open should count it as playing: %q", tt.Texts())
	}
	release()
	drain()
	if strings.Join(f.opened, ",") != "/movies/a.mkv" || strings.Join(f.added, ",") != "/movies/a.srt" {
		t.Errorf("opened %v, added %v; want the video, then its subtitles", f.opened, f.added)
	}
}

func TestCompactMoreMenuStaysInTheWindow(t *testing.T) {
	f := newFakePlayer()
	a := newDemoApp(f, func(c *ui.Context) ui.Element { return ui.Box(c).Background(stageBlack) })
	tt := ui.NewTesterWith(a.view, 320, 400, ui.TesterOptions{Preferences: ui.Preferences{Pointer: ui.PointerCoarse, TextScale: 1}})
	tt.Frame()
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	tt.Advance(time.Second)
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !visible(tt, "Speed: Normal") || !visible(tt, "Audio: English") || !visible(tt, "Choose Video…") {
		t.Fatalf("compact More menu lacks speed and audio: %q", tt.Texts())
	}
	for _, n := range tt.AccessAll() {
		if n.Role != ui.RoleMenuItem && n.Role != ui.RoleMenuItemCheckBox {
			continue
		}
		if n.Bounds.X < 0 || n.Bounds.X+n.Bounds.W > 320.5 || n.Bounds.Y < 0 || n.Bounds.Y+n.Bounds.H > 400.5 {
			t.Errorf("%q outside the window: %+v", n.Name, n.Bounds)
		}
	}
	snapshot(t, tt, "more-compact")
	if err := tt.Click("Speed: Normal"); err != nil {
		t.Fatal(err)
	}
	if f.snap.Speed != 1.25 {
		t.Errorf("stepping the speed gave %v, want 1.25", f.snap.Speed)
	}
}

func TestOpenCompletionsSpeakForThemselves(t *testing.T) {
	a, _, tt := newTestApp(t, 960, 600)
	q, release, drain := heldQueue()
	a.cmds = q
	a.open("/movies/b.mkv", nil)
	a.open("https://x.test/a.mkv?token=SECRET", nil)
	a.open("/movies/b.mkv", nil)
	release()
	// The first two finish while the last is still queued behind them.
	q.push("", func() {})
	drain()
	tt.Frame()
	if a.opening {
		t.Error("the last open finished but still counts as opening")
	}
}

func TestSubtitleTogglesWhileBusyAreKept(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	q, release, drain := heldQueue()
	a.cmds = q
	tt.Key(0, ui.KeyC)
	tt.Advance(900 * time.Millisecond) // longer than the hold, still queued
	tt.Key(0, ui.KeyC)
	release()
	drain()
	tt.Frame()
	if f.snap.SubTrackID == 0 {
		t.Error("off then on while libmpv was busy left subtitles off")
	}
}

func TestDraggingOffTheSeekBarStaysThrottled(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	info, _ := tt.Access("Playback position")
	b := info.Bounds
	y := b.Y + b.H/2
	tt.Press(b.X+10, y)
	for i := 1; i <= 10; i++ {
		tt.Move(b.X+10+float32(i)*30, y-40) // well above the bar
		tt.Advance(16 * time.Millisecond)
	}
	if !a.scrubbing {
		t.Error("leaving the bar while dragging ended the scrub")
	}
	tt.Release(b.X+310, y-40)
	tt.Frame()
	if len(f.seeks) > 6 {
		t.Errorf("%d seeks for a 10-move drag off the bar; want throttled", len(f.seeks))
	}
	if a.scrubbing {
		t.Error("scrub did not end on release")
	}
}

func TestTabRevealsHiddenControls(t *testing.T) {
	a, _, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	tt.Advance(hideChromeAfter + time.Second)
	if visible(tt, "Pause") {
		t.Fatal("controls should be hidden")
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !visible(tt, "Pause") || !tt.Focused("Pause") {
		t.Fatalf("Tab should bring the controls back with the focus in them (focused Pause: %v)", tt.Focused("Pause"))
	}
	tt.Advance(hideChromeAfter + time.Second)
	if !visible(tt, "Pause") {
		t.Error("controls should stay while the keyboard is in them")
	}
}

func TestSilentVolumeUnmutes(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	f.snap.Volume = 0 // dragged to zero: silent but not muted
	tt.Frame()
	if err := tt.Click("Unmute"); err != nil {
		t.Fatal(err)
	}
	if f.snap.Muted || f.snap.Volume <= 0 {
		t.Errorf("“Unmute” left it silent: muted %v volume %v", f.snap.Muted, f.snap.Volume)
	}
}

func TestMenuClosedByResizeDoesNotStickShortcuts(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	if err := tt.Click("Playback Speed"); err != nil {
		t.Fatal(err)
	}
	if !a.speedOpen {
		t.Fatal("speed menu did not open")
	}
	tt.SetSize(320, 600)
	tt.Frame()
	tt.Frame()
	if a.anyMenuOpen() {
		t.Fatal("a menu whose button went away still counts as open")
	}
	tt.Key(0, ui.KeyK)
	if !f.snap.Paused {
		t.Error("K should pause once the menu is gone")
	}
}

func TestUnrelatedDropKeepsPlayback(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	tt.DropFiles(480, 300, "/docs/readme.txt")
	tt.Frame()
	if len(f.opened) != 1 || !tt.HasText("Can’t open “readme.txt”") {
		t.Errorf("a text file replaced the movie or went unexplained: opened %v", f.opened)
	}
	tt.DropFiles(480, 300, "/docs/readme.txt", "/movies/next.mkv")
	if f.opened[len(f.opened)-1] != "/movies/next.mkv" {
		t.Errorf("mixed drop opened %q, want the video", f.opened[len(f.opened)-1])
	}
}

func TestAudioOnlyKeepsControls(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("/music/song.flac", nil)
	f.snap.VideoWidth, f.snap.MediaTitle = 0, "A Song"
	f.snap.Tracks = filterByType(f.snap.Tracks, player.TrackAudio)
	tt.Frame()
	tt.Advance(hideChromeAfter + time.Second)
	if !visible(tt, "Pause") || !tt.HasText("A Song") {
		t.Errorf("audio-only playback should keep its controls and title: %q", tt.Texts())
	}
}

func TestLiveStreamHasNoSeeking(t *testing.T) {
	a, f, tt := newTestApp(t, 960, 600)
	a.open("https://live.test/stream.m3u8", nil)
	f.snap.Duration, f.snap.Seekable = 3*time.Second, false // libmpv's buffered "duration"
	tt.Frame()
	if visible(tt, "Back 5 Seconds") || tt.HasText("-0:00") || tt.HasText("-0:03") {
		t.Errorf("live stream shows seeking it can't do: %q", tt.Texts())
	}
	pos := f.snap.Position
	tt.Key(0, ui.KeyRight)
	if f.snap.Position != pos {
		t.Error("Right skipped in a live stream")
	}
}

// TestMotionFrames writes the frames of the main transitions for review
// when PLAYER_SNAPSHOT_DIR is set: the welcome card giving way to the
// video, and the controls hiding and coming back.
func TestMotionFrames(t *testing.T) {
	if os.Getenv("PLAYER_SNAPSHOT_DIR") == "" {
		t.Skip("set PLAYER_SNAPSHOT_DIR to write motion frames")
	}
	a, _, tt := newTestApp(t, 640, 400)
	tt.Advance(time.Second)
	a.open("/movies/movie.mkv", nil)
	tt.Frame()
	for i := 0; i <= 6; i++ {
		snapshot(t, tt, fmt.Sprintf("open-%03dms", i*50))
		tt.Advance(50 * time.Millisecond)
	}
	tt.Move(320, 100)
	tt.Advance(hideChromeAfter)
	for i := 0; i <= 8; i++ {
		snapshot(t, tt, fmt.Sprintf("hide-%03dms", i*50))
		tt.Advance(50 * time.Millisecond)
	}
	tt.Move(330, 100)
	for i := 0; i <= 5; i++ {
		snapshot(t, tt, fmt.Sprintf("show-%03dms", i*40))
		tt.Advance(40 * time.Millisecond)
	}
	if err := tt.Click("Subtitles"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 5; i++ {
		snapshot(t, tt, fmt.Sprintf("menu-%03dms", i*40))
		tt.Advance(40 * time.Millisecond)
	}
}
