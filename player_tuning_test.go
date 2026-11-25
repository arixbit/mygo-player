package player

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func mpvFloat(t *testing.T, p *Player, name string) float64 {
	t.Helper()
	raw, err := p.GetPropertyString(name)
	if err != nil {
		t.Fatalf("GetPropertyString(%s): %v", name, err)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", name, raw, err)
	}
	return v
}

func TestTuningMethodsReturnErrClosed(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	p, err := New(Options{AudioOutput: "null", HWDec: HWDecNone})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	calls := map[string]func() error{
		"SetSubtitleDelay": func() error { return p.SetSubtitleDelay(time.Second) },
		"SetAudioDelay":    func() error { return p.SetAudioDelay(time.Second) },
		"SetSubtitleScale": func() error { return p.SetSubtitleScale(1.2) },
		"SetSubtitlePos":   func() error { return p.SetSubtitlePos(90) },
		"SetVideoRotate":   func() error { return p.SetVideoRotate(90) },
		"SetPanscan":       func() error { return p.SetPanscan(0.5) },
		"FrameStep":        p.FrameStep,
		"FrameBackStep":    p.FrameBackStep,
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}
	}
}

func TestTuningInvalidValuesRejected(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	p := newTestPlayer(t, Options{})
	before := p.Snapshot()
	if before.SubScale != 1.0 || before.SubPos != 100 || before.VideoRotate != 0 || before.Panscan != 0 {
		t.Fatalf("initial tuning snapshot = scale %v pos %d rotate %d panscan %v", before.SubScale, before.SubPos, before.VideoRotate, before.Panscan)
	}
	bad := map[string]error{
		"scale 0":       p.SetSubtitleScale(0),
		"scale -1":      p.SetSubtitleScale(-1),
		"scale NaN":     p.SetSubtitleScale(math.NaN()),
		"scale 101":     p.SetSubtitleScale(101),
		"scale +Inf":    p.SetSubtitleScale(math.Inf(1)),
		"pos -1":        p.SetSubtitlePos(-1),
		"pos 151":       p.SetSubtitlePos(151),
		"rotate 45":     p.SetVideoRotate(45),
		"rotate -90":    p.SetVideoRotate(-90),
		"rotate 360":    p.SetVideoRotate(360),
		"panscan -0.1":  p.SetPanscan(-0.1),
		"panscan 1.01":  p.SetPanscan(1.01),
		"panscan NaN":   p.SetPanscan(math.NaN()),
		"frame no file": p.FrameStep(),
	}
	for name, err := range bad {
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	time.Sleep(200 * time.Millisecond)
	after := p.Snapshot()
	if after.SubScale != 1.0 || after.SubPos != 100 || after.VideoRotate != 0 || after.Panscan != 0 {
		t.Fatalf("invalid values leaked into snapshot: scale %v pos %d rotate %d panscan %v", after.SubScale, after.SubPos, after.VideoRotate, after.Panscan)
	}
	if v := mpvFloat(t, p, "sub-scale"); v != 1 {
		t.Fatalf("mpv sub-scale = %v after invalid sets", v)
	}
	if v := mpvFloat(t, p, "panscan"); v != 0 {
		t.Fatalf("mpv panscan = %v after invalid sets", v)
	}
}

func TestTuningSubtitleAndAudioDelay(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	p := newTestPlayer(t, Options{})
	if err := p.SetSubtitleDelay(250 * time.Millisecond); err != nil {
		t.Fatalf("SetSubtitleDelay: %v", err)
	}
	if err := p.SetAudioDelay(-120 * time.Millisecond); err != nil {
		t.Fatalf("SetAudioDelay: %v", err)
	}
	if s := p.Snapshot(); s.SubDelay != 250*time.Millisecond || s.AudioDelay != -120*time.Millisecond {
		t.Fatalf("snapshot SubDelay=%v AudioDelay=%v", s.SubDelay, s.AudioDelay)
	}
	if v := mpvFloat(t, p, "sub-delay"); math.Abs(v-0.25) > 1e-9 {
		t.Fatalf("mpv sub-delay = %v", v)
	}
	if v := mpvFloat(t, p, "audio-delay"); math.Abs(v+0.12) > 1e-9 {
		t.Fatalf("mpv audio-delay = %v", v)
	}
	// Observation keeps the snapshot in sync with changes made elsewhere.
	if err := p.SetPropertyString("sub-delay", "-1.5"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPropertyString("audio-delay", "0.3"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "observed delays", func() bool {
		s := p.Snapshot()
		return s.SubDelay == -1500*time.Millisecond && s.AudioDelay == 300*time.Millisecond
	})
}

func TestTuningSubtitleScaleAndPos(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	p := newTestPlayer(t, Options{})
	if err := p.SetSubtitleScale(1.5); err != nil {
		t.Fatalf("SetSubtitleScale: %v", err)
	}
	if err := p.SetSubtitlePos(80); err != nil {
		t.Fatalf("SetSubtitlePos: %v", err)
	}
	if s := p.Snapshot(); s.SubScale != 1.5 || s.SubPos != 80 {
		t.Fatalf("snapshot SubScale=%v SubPos=%d", s.SubScale, s.SubPos)
	}
	if v := mpvFloat(t, p, "sub-scale"); v != 1.5 {
		t.Fatalf("mpv sub-scale = %v", v)
	}
	if v := mpvFloat(t, p, "sub-pos"); v != 80 {
		t.Fatalf("mpv sub-pos = %v", v)
	}
	for _, edge := range []int{0, 150} {
		if err := p.SetSubtitlePos(edge); err != nil {
			t.Fatalf("SetSubtitlePos(%d): %v", edge, err)
		}
	}
	if err := p.SetPropertyString("sub-scale", "2.25"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPropertyString("sub-pos", "42"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "observed sub-scale/sub-pos", func() bool {
		s := p.Snapshot()
		return s.SubScale == 2.25 && s.SubPos == 42
	})
}

func TestTuningVideoRotate(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "wide.mkv", clipSpec{dur: 3, w: 240, h: 120})
	p := newTestPlayer(t, Options{})
	if err := p.Open(clip, OpenOptions{Paused: true}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "loaded", func() bool { return p.Snapshot().State == StatePaused })
	for _, deg := range []int{90, 180, 270, 0} {
		if err := p.SetVideoRotate(deg); err != nil {
			t.Fatalf("SetVideoRotate(%d): %v", deg, err)
		}
		if s := p.Snapshot(); s.VideoRotate != deg {
			t.Fatalf("snapshot VideoRotate = %d, want %d", s.VideoRotate, deg)
		}
		if raw, _ := p.GetPropertyString("video-rotate"); raw != strconv.Itoa(deg) {
			t.Fatalf("mpv video-rotate = %q, want %d", raw, deg)
		}
	}
	if err := p.SetPropertyString("video-rotate", "180"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "observed video-rotate", func() bool { return p.Snapshot().VideoRotate == 180 })
}

func TestTuningPanscanFillsSquareView(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	// 2:1 solid red video inside a 120x120 View: letterboxed by default
	// (black top/bottom), fully red once SetPanscan(1.0) crops to fill.
	clip := genClip(t, ffmpeg, "red-2x1.mkv", clipSpec{dur: 5, w: 240, h: 120, color: "red"})
	p := newTestPlayer(t, Options{})
	if err := p.Open(clip, OpenOptions{Paused: true}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "first frame", func() bool {
		w, h := p.FrameSize()
		return p.FramesRendered() > 0 && w > 0 && h > 0
	})
	tt := ui.NewTester(func(c *ui.Context) {
		View(c, p).Fill()
	}, 120, 120)
	isRed := func(x, y int) bool {
		c := tt.Image().RGBAAt(x, y)
		return c.R >= 180 && c.G <= 60 && c.B <= 60
	}
	isBlack := func(x, y int) bool {
		c := tt.Image().RGBAAt(x, y)
		return c.R <= 30 && c.G <= 30 && c.B <= 30
	}
	waitFor(t, 5*time.Second, "letterboxed: black top, red center", func() bool {
		tt.Frame()
		return isBlack(60, 5) && isRed(60, 60)
	})
	if err := p.SetPanscan(1.0); err != nil {
		t.Fatalf("SetPanscan(1.0): %v", err)
	}
	if s := p.Snapshot(); s.Panscan != 1.0 {
		t.Fatalf("snapshot Panscan = %v", s.Panscan)
	}
	waitFor(t, 5*time.Second, "panscan fill: red top pixel", func() bool {
		tt.Frame()
		return isRed(60, 5) && isRed(60, 115) && isRed(60, 60)
	})
	c := tt.Image().RGBAAt(60, 5)
	t.Logf("top pixel after SetPanscan(1.0): (%d,%d,%d,%d)", c.R, c.G, c.B, c.A)
	if err := p.SetPropertyString("panscan", "0.25"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "observed panscan", func() bool { return p.Snapshot().Panscan == 0.25 })
}

func TestTuningFrameStepAndBackStep(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "steps.mkv", clipSpec{dur: 4, fps: 10, gop: 5})
	p := newTestPlayer(t, Options{})
	if err := p.Open(clip, OpenOptions{Paused: true}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "loaded paused", func() bool { return p.Snapshot().State == StatePaused })
	if err := p.Seek(time.Second); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	waitFor(t, 4*time.Second, "at 1s", func() bool {
		pos := p.Snapshot().Position
		return pos >= 950*time.Millisecond && pos <= 1050*time.Millisecond
	})
	p0 := p.Snapshot().Position
	if err := p.FrameStep(); err != nil {
		t.Fatalf("FrameStep: %v", err)
	}
	waitFor(t, 4*time.Second, "advanced one frame", func() bool {
		return p.Snapshot().Position >= p0+90*time.Millisecond
	})
	p1 := p.Snapshot().Position
	if d := p1 - p0; d > 150*time.Millisecond {
		t.Fatalf("FrameStep advanced %v, want ~100ms (one 10fps frame)", d)
	}
	if !p.Snapshot().Paused {
		t.Fatal("FrameStep left playback running")
	}
	if err := p.FrameBackStep(); err != nil {
		t.Fatalf("FrameBackStep: %v", err)
	}
	waitFor(t, 4*time.Second, "stepped back one frame", func() bool {
		return p.Snapshot().Position <= p1-90*time.Millisecond
	})
	if d := p1 - p.Snapshot().Position; d > 150*time.Millisecond {
		t.Fatalf("FrameBackStep moved back %v, want ~100ms", d)
	}
	t.Logf("frame positions: start=%v step=%v back=%v", p0, p1, p.Snapshot().Position)
}

func TestToggleMute(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	p := newTestPlayer(t, Options{Volume: 40})
	if err := p.ToggleMute(); err != nil {
		t.Fatalf("ToggleMute: %v", err)
	}
	waitFor(t, 3*time.Second, "muted", func() bool { return p.Snapshot().Muted })
	if v := p.Snapshot().Volume; v != 40 {
		t.Fatalf("muting changed the volume to %v", v)
	}
	if err := p.ToggleMute(); err != nil {
		t.Fatalf("ToggleMute: %v", err)
	}
	waitFor(t, 3*time.Second, "unmuted at 40", func() bool {
		s := p.Snapshot()
		return !s.Muted && s.Volume == 40
	})

	// Unmuting a silent player restores an audible volume.
	if err := p.SetVolume(0); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "muted at 0", func() bool {
		s := p.Snapshot()
		return s.Muted && s.Volume == 0
	})
	if err := p.ToggleMute(); err != nil {
		t.Fatalf("ToggleMute: %v", err)
	}
	waitFor(t, 3*time.Second, "unmuted audible", func() bool {
		s := p.Snapshot()
		return !s.Muted && s.Volume == 50
	})
	// Calls in quick succession each flip it, whatever Snapshot says yet.
	for i := 0; i < 10; i++ {
		if err := p.ToggleMute(); err != nil {
			t.Fatal(err)
		}
		if err := p.ToggleMute(); err != nil {
			t.Fatal(err)
		}
	}
	if m, _ := p.GetPropertyString("mute"); m != "no" {
		t.Fatalf("mute after ten quick toggle pairs = %q, want no", m)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.ToggleMute(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ToggleMute after Close = %v, want ErrClosed", err)
	}
}

func TestTogglePauseQuickPairsAndSeekable(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "toggle.mkv", clipSpec{dur: 10})
	p := newTestPlayer(t, Options{})
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "playing and seekable", func() bool {
		s := p.Snapshot()
		return s.State == StatePlaying && s.Seekable
	})
	// Calls in quick succession each flip it, whatever Snapshot says yet.
	for i := 0; i < 10; i++ {
		if err := p.TogglePause(); err != nil {
			t.Fatal(err)
		}
		if err := p.TogglePause(); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := p.GetPropertyString("pause"); v != "no" {
		t.Fatalf("pause after ten quick toggle pairs = %q, want no", v)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if p.Snapshot().Seekable {
		t.Error("Seekable should reset when the media is stopped")
	}
}

func TestTogglePausePairAtEOF(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "eof-toggle.mkv", clipSpec{dur: 1})
	p := newTestPlayer(t, Options{})
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "EOF", func() bool { return p.Snapshot().State == StateEnded })
	// Replay, then pause right away: the second press must pause.
	if err := p.TogglePause(); err != nil {
		t.Fatal(err)
	}
	if err := p.TogglePause(); err != nil {
		t.Fatal(err)
	}
	if v, _ := p.GetPropertyString("pause"); v != "yes" {
		t.Fatalf("pause after replay+toggle at EOF = %q, want yes", v)
	}
}
