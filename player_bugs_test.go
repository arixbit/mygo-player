package player

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arixbit/mygo-player/internal/mpv"
)

// clipSpec describes a synthetic single-color H.264 + AAC fixture.
type clipSpec struct {
	w, h  int
	fps   int
	dur   float64
	color string
	gop   int
}

func genClip(t *testing.T, ffmpeg, name string, c clipSpec) string {
	t.Helper()
	if c.w == 0 {
		c.w, c.h = 320, 180
	}
	if c.fps == 0 {
		c.fps = 24
	}
	if c.color == "" {
		c.color = "red"
	}
	if c.gop == 0 {
		c.gop = 12
	}
	path := filepath.Join(t.TempDir(), name)
	cmd := exec.Command(ffmpeg,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=%g", c.color, c.w, c.h, c.fps, c.dur),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%g", c.dur),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", strconv.Itoa(c.gop),
		"-c:a", "aac", "-b:a", "64k", "-shortest",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate %s failed: %v\n%s", name, err, out)
	}
	return path
}

// newTestPlayer creates a headless (null audio, software decode) Player that
// is closed at test cleanup.
func newTestPlayer(t *testing.T, opt Options) *Player {
	t.Helper()
	if opt.AudioOutput == "" {
		opt.AudioOutput = "null"
	}
	if opt.HWDec == "" {
		opt.HWDec = HWDecNone
	}
	p, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// --- Bug 1: EOF handling ---

func TestBug1KeepOpenEOFCallbackOnceAndReplay(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "short.mkv", clipSpec{dur: 1})

	var ends, endErrs atomic.Int32
	p := newTestPlayer(t, Options{
		// keep-open-pause=no keeps Paused=false at EOF, so TogglePause must
		// rely on Snapshot().EOF to replay rather than pausing.
		ExtraOptions: map[string]string{"keep-open-pause": "no"},
		OnEndFile: func(err error) {
			ends.Add(1)
			if err != nil {
				endErrs.Add(1)
			}
		},
	})
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "keep-open EOF callback", func() bool {
		s := p.Snapshot()
		return s.EOF && s.State == StateEnded && ends.Load() == 1
	})
	time.Sleep(400 * time.Millisecond)
	if n := ends.Load(); n != 1 {
		t.Fatalf("OnEndFile called %d times at first EOF, want exactly 1", n)
	}
	if s := p.Snapshot(); s.Paused {
		t.Fatalf("expected Paused=false at EOF with keep-open-pause=no, got %+v", s)
	}

	// TogglePause at EOF (not paused) must replay from the start.
	if err := p.TogglePause(); err != nil {
		t.Fatalf("TogglePause at EOF: %v", err)
	}
	waitFor(t, 4*time.Second, "replay after EOF", func() bool {
		s := p.Snapshot()
		return !s.EOF && s.State == StatePlaying
	})
	// Reaching EOF again re-delivers the callback exactly once.
	waitFor(t, 8*time.Second, "second keep-open EOF callback", func() bool {
		return ends.Load() == 2 && p.Snapshot().EOF
	})

	// A later Stop of the already-reported file must not report it again.
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := ends.Load(); n != 2 {
		t.Fatalf("OnEndFile called %d times after Stop, want 2 (no re-send)", n)
	}
	if n := endErrs.Load(); n != 0 {
		t.Fatalf("OnEndFile received %d errors, want 0", n)
	}
}

func TestBug1KeepOpenPausedTogglePauseReplays(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "short.mkv", clipSpec{dur: 1})
	var ends atomic.Int32
	p := newTestPlayer(t, Options{OnEndFile: func(error) { ends.Add(1) }})
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "EOF (default keep-open)", func() bool {
		return p.Snapshot().State == StateEnded && ends.Load() == 1
	})
	if err := p.TogglePause(); err != nil {
		t.Fatalf("TogglePause: %v", err)
	}
	waitFor(t, 4*time.Second, "playing after replay", func() bool {
		s := p.Snapshot()
		return s.State == StatePlaying && !s.EOF && !s.Paused
	})
}

func TestBug1NoKeepOpenEOFSingleCallbackAndReplay(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "short.mkv", clipSpec{dur: 1})
	keepOpen := false
	var ends atomic.Int32
	p := newTestPlayer(t, Options{KeepOpen: &keepOpen, OnEndFile: func(err error) {
		if err != nil {
			t.Errorf("OnEndFile(%v), want nil", err)
		}
		ends.Add(1)
	}})
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "keep-open=no EOF", func() bool {
		s := p.Snapshot()
		return s.EOF && s.State == StateEnded && ends.Load() == 1
	})
	time.Sleep(400 * time.Millisecond)
	if n := ends.Load(); n != 1 {
		t.Fatalf("OnEndFile called %d times with keep-open=no, want exactly 1", n)
	}
	if err := p.TogglePause(); err != nil {
		t.Fatalf("TogglePause after unloaded EOF: %v", err)
	}
	waitFor(t, 4*time.Second, "replay after keep-open=no EOF", func() bool {
		s := p.Snapshot()
		return s.State == StatePlaying && !s.EOF
	})
	waitFor(t, 8*time.Second, "second EOF", func() bool { return ends.Load() == 2 })
}

// --- Bug 2: Close inside callbacks ---

// closeFromCallback runs a player whose callback (selected by install) calls
// Close, and verifies Close returns within 3s, the event loop exits, the mpv
// handle is released, and the Player reports ErrClosed afterwards.
func closeFromCallback(t *testing.T, clip string, keepOpen bool, install func(opt *Options, trigger func(), cur func() *Player)) {
	t.Helper()
	var pp atomic.Pointer[Player]
	type result struct {
		err     error
		onEvent bool
	}
	started := make(chan struct{})
	done := make(chan result, 1)
	var once sync.Once
	trigger := func() {
		p := pp.Load()
		if p == nil {
			return
		}
		once.Do(func() {
			onEvent := p.onEventGoroutine()
			close(started)
			done <- result{err: p.Close(), onEvent: onEvent}
		})
	}
	opt := Options{AudioOutput: "null", HWDec: HWDecNone, KeepOpen: &keepOpen}
	install(&opt, trigger, pp.Load)
	p, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pp.Store(p)
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("callback that calls Close was never invoked")
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Close inside callback returned %v", r.err)
		}
		if !r.onEvent {
			t.Fatal("callback did not run on the event goroutine; test would not cover re-entrancy")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close inside callback deadlocked (no return within 3s)")
	}
	select {
	case <-p.eventDone:
	case <-time.After(3 * time.Second):
		t.Fatal("event loop did not exit within 3s after Close in callback")
	}
	if h := p.client.Handle(); h != 0 {
		t.Fatalf("mpv handle %#x not released after event loop exit", h)
	}
	if err := p.Play(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Play after Close = %v, want ErrClosed", err)
	}
	if err := p.SetPanscan(0.5); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetPanscan after Close = %v, want ErrClosed", err)
	}
	_ = p.Snapshot() // must not touch freed state
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("second Close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second Close blocked")
	}
}

func TestBug2CloseInsideOnStateChange(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "clip.mkv", clipSpec{dur: 3})
	closeFromCallback(t, clip, true, func(opt *Options, trigger func(), cur func() *Player) {
		opt.OnStateChange = func() {
			// Close once playback is running (state set by the event loop).
			if p := cur(); p != nil && p.Snapshot().State == StatePlaying {
				trigger()
			}
		}
	})
}

func TestBug2CloseInsideOnEndFileKeepOpen(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "short.mkv", clipSpec{dur: 1})
	closeFromCallback(t, clip, true, func(opt *Options, trigger func(), _ func() *Player) {
		opt.OnEndFile = func(error) { trigger() }
	})
}

func TestBug2CloseInsideOnEndFileNoKeepOpen(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "short.mkv", clipSpec{dur: 1})
	closeFromCallback(t, clip, false, func(opt *Options, trigger func(), _ func() *Player) {
		opt.OnEndFile = func(error) { trigger() }
	})
}

func TestBug2PlayerMethodsInsideOnStateChange(t *testing.T) {
	// Callbacks fired synchronously from Open/track methods must not hold
	// internal locks: calling Player methods from them must not deadlock.
	ffmpeg := requireLibMPVAndFFmpeg(t)
	tmp := t.TempDir()
	mkvPath, _ := generateTestMKV(t, ffmpeg, tmp)
	var pp atomic.Pointer[Player]
	var calls atomic.Int32
	p := newTestPlayer(t, Options{OnStateChange: func() {
		if p := pp.Load(); p != nil {
			calls.Add(1)
			_ = p.SetSpeed(1.0)
		}
	}})
	pp.Store(p)
	done := make(chan error, 1)
	go func() {
		if err := p.Open(mkvPath); err != nil {
			done <- err
			return
		}
		waitFor(t, 8*time.Second, "audio tracks", func() bool { return len(p.AudioTracks()) == 2 })
		done <- p.SelectAudioTrack(p.AudioTracks()[1].ID)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open/SelectAudioTrack: %v", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("Player method inside OnStateChange deadlocked")
	}
	if calls.Load() == 0 {
		t.Fatal("OnStateChange never called")
	}
}

// --- Bug 3: state reset on Stop / Open ---

func assertMediaCleared(t *testing.T, when string, s Snapshot) {
	t.Helper()
	if s.EOF || s.Position != 0 || s.Duration != 0 || s.Buffering || s.MediaTitle != "" ||
		s.VideoWidth != 0 || s.VideoHeight != 0 || s.AudioTrackID != 0 || s.SubTrackID != 0 ||
		len(s.Tracks) != 0 || s.Err != nil {
		t.Fatalf("%s: per-file state not cleared: %+v", when, s)
	}
}

func TestBug3StopClearsSnapshot(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	mkvPath, _ := generateTestMKV(t, ffmpeg, t.TempDir())
	p := newTestPlayer(t, Options{})
	if err := p.Open(mkvPath); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "subtitle track listed", func() bool { return len(p.SubtitleTracks()) > 0 })
	if err := p.SelectSubtitleTrack(p.SubtitleTracks()[0].ID); err != nil {
		t.Fatalf("SelectSubtitleTrack: %v", err)
	}
	waitFor(t, 8*time.Second, "media fully populated", func() bool {
		s := p.Snapshot()
		return len(s.Tracks) > 0 && s.VideoWidth > 0 && s.Duration > 0 && s.Position > 200*time.Millisecond &&
			s.MediaTitle != "" && s.AudioTrackID != 0 && s.SubTrackID != 0
	})
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	s := p.Snapshot()
	if s.State != StateIdle {
		t.Fatalf("State after Stop = %v, want idle", s.State)
	}
	assertMediaCleared(t, "immediately after Stop", s)
	// Late libmpv events must not resurrect stale values.
	time.Sleep(600 * time.Millisecond)
	s = p.Snapshot()
	if s.State != StateIdle {
		t.Fatalf("State 600ms after Stop = %v, want idle", s.State)
	}
	assertMediaCleared(t, "600ms after Stop", s)
}

func TestBug3OpenClearsPreviousMediaAndSyncsPaused(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	mkvPath, _ := generateTestMKV(t, ffmpeg, t.TempDir())
	clip := genClip(t, ffmpeg, "second.mkv", clipSpec{dur: 3, color: "blue", w: 160, h: 90})
	p := newTestPlayer(t, Options{})
	if err := p.Open(mkvPath); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "first media playing", func() bool {
		s := p.Snapshot()
		return len(s.Tracks) > 0 && s.Position > 300*time.Millisecond && s.VideoWidth == 320
	})
	if err := p.Open(clip, OpenOptions{Paused: true}); err != nil {
		t.Fatalf("Open(second, paused): %v", err)
	}
	s := p.Snapshot()
	if !s.Paused || s.State != StateLoading {
		t.Fatalf("after Open(Paused:true): Paused=%v State=%v, want true/loading", s.Paused, s.State)
	}
	assertMediaCleared(t, "immediately after Open", s)
	waitFor(t, 8*time.Second, "second media loaded paused", func() bool {
		s := p.Snapshot()
		return s.State == StatePaused && s.Paused && s.VideoWidth == 160 && s.Duration > 0
	})
	if err := p.Open(clip, OpenOptions{Paused: false}); err != nil {
		t.Fatalf("Open(second, unpaused): %v", err)
	}
	if s := p.Snapshot(); s.Paused {
		t.Fatal("Snapshot().Paused still true right after Open(Paused:false)")
	}
	waitFor(t, 8*time.Second, "playing after Open(Paused:false)", func() bool {
		return p.Snapshot().State == StatePlaying
	})
}

// --- Bug 4: StartMuted keeps an audible volume ---

func TestBug4StartMutedKeepsVolume(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	for _, tc := range []struct {
		vol, want float64
	}{{0, 100}, {-5, 100}, {40, 40}} {
		p := newTestPlayer(t, Options{StartMuted: true, Volume: tc.vol})
		s := p.Snapshot()
		if !s.Muted || s.Volume != tc.want {
			t.Fatalf("Volume=%v StartMuted: snapshot Muted=%v Volume=%v, want true/%v", tc.vol, s.Muted, s.Volume, tc.want)
		}
		raw, err := p.GetPropertyString("volume")
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := strconv.ParseFloat(raw, 64); v != tc.want {
			t.Fatalf("mpv volume = %s, want %v", raw, tc.want)
		}
		if m, _ := p.GetPropertyString("mute"); m != "yes" {
			t.Fatalf("mpv mute = %q, want yes", m)
		}
		if err := p.SetMuted(false); err != nil {
			t.Fatalf("SetMuted(false): %v", err)
		}
		waitFor(t, 3*time.Second, "unmuted audible", func() bool {
			s := p.Snapshot()
			return !s.Muted && s.Volume == tc.want
		})
		if m, _ := p.GetPropertyString("mute"); m != "no" {
			t.Fatalf("mpv mute after unmute = %q, want no", m)
		}
	}
}

// --- Bug 5: readable playback errors ---

func TestBug5ReadableEndFileError(t *testing.T) {
	requireLibMPVAndFFmpeg(t)
	if got := mpv.ErrorString(mpv.ErrLoadingFailed); got != "loading failed" {
		t.Fatalf("mpv.ErrorString(-13) = %q", got)
	}
	errCh := make(chan error, 4)
	p := newTestPlayer(t, Options{OnEndFile: func(err error) { errCh <- err }})
	missing := filepath.Join(t.TempDir(), "does-not-exist.mkv")
	if err := p.Open(missing); err != nil {
		t.Fatalf("Open: %v", err)
	}
	var endErr error
	select {
	case endErr = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no OnEndFile for missing file")
	}
	if endErr == nil {
		t.Fatal("OnEndFile(nil) for missing file, want error")
	}
	msg := endErr.Error()
	t.Logf("end-file error: %q", msg)
	var mErr *mpv.Error
	if !errors.As(endErr, &mErr) {
		t.Fatalf("error %T is not *mpv.Error", endErr)
	}
	if strings.Contains(msg, "end file error") || strings.Count(msg, fmt.Sprintf("(%d)", mErr.Code)) != 1 {
		t.Fatalf("unreadable error message %q", msg)
	}
	if mErr.Code != mpv.ErrLoadingFailed || msg != "mpv playback: loading failed (-13)" {
		t.Fatalf("error = %q (code %d), want \"mpv playback: loading failed (-13)\"", msg, mErr.Code)
	}
	waitFor(t, 2*time.Second, "error state", func() bool {
		s := p.Snapshot()
		return s.State == StateError && s.Err != nil && s.Err.Error() == msg
	})
}

// --- Bug 6: HTTP header sanitizing and case-insensitive merge ---

func TestBug6SplitHTTPHeadersSanitizesKeys(t *testing.T) {
	fields, ua, ref := splitHTTPHeaders(map[string]string{
		"X-Evil\r\nInjected": "1",
		"Bad:Key":            "2",
		" : \r\n ":           "dropped",
		"":                   "dropped",
		"X-Val":              "a\r\nb",
		"user-agent":         "LowerUA",
	}, "", "")
	want := []string{"BadKey: 2", "X-EvilInjected: 1", "X-Val: ab"}
	slices.Sort(fields)
	if !slices.Equal(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	if ua != "LowerUA" || ref != "" {
		t.Fatalf("ua=%q ref=%q", ua, ref)
	}
	for _, f := range fields {
		if strings.ContainsAny(f, "\r\n") || strings.Count(f, ":") != 1 {
			t.Fatalf("unsafe header field %q", f)
		}
	}
}

func TestBug6MergeHeadersCaseInsensitive(t *testing.T) {
	got := mergeHeaders(
		map[string]string{"Authorization": "Bearer default", "X-Keep": "k", "X-Token": "d"},
		map[string]string{"authorization": "Bearer open", "X-TOKEN:": "o"},
	)
	fields, _, _ := splitHTTPHeaders(got, "", "")
	slices.Sort(fields)
	want := []string{"X-Keep: k", "X-TOKEN: o", "authorization: Bearer open"}
	if !slices.Equal(fields, want) {
		t.Fatalf("merged fields = %#v, want %#v", fields, want)
	}
	if m := mergeHeaders(nil, nil); len(m) != 0 {
		t.Fatalf("mergeHeaders(nil, nil) = %v", m)
	}
}

func TestBug6OpenHeadersOverrideOverHTTP(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "net.mkv", clipSpec{dur: 2})
	var mu sync.Mutex
	seen := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if _, ok := seen[r.URL.Path]; !ok {
			seen[r.URL.Path] = r.Header.Clone()
		}
		mu.Unlock()
		http.ServeFile(w, r, clip)
	}))
	defer srv.Close()

	p := newTestPlayer(t, Options{HTTPHeaders: map[string]string{
		"X-Token":       "default",
		"Authorization": "Bearer default",
	}})
	if err := p.Open(srv.URL+"/a.mkv", OpenOptions{Headers: map[string]string{
		"x-token":          "per-open",
		"X-Bad\r\nEvil:":   "v",
		"\r\n":             "dropped",
		"x-extra-per-open": "1",
	}}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "HTTP request", func() bool {
		mu.Lock()
		defer mu.Unlock()
		_, ok := seen["/a.mkv"]
		return ok
	})
	mu.Lock()
	h := seen["/a.mkv"]
	mu.Unlock()
	if got := h.Values("X-Token"); len(got) != 1 || got[0] != "per-open" {
		t.Fatalf("X-Token values = %q, want exactly [per-open]", got)
	}
	if got := h.Get("Authorization"); got != "Bearer default" {
		t.Fatalf("Authorization = %q, want default", got)
	}
	if got := h.Get("X-BadEvil"); got != "v" {
		t.Fatalf("sanitized header X-BadEvil = %q, want v (headers: %v)", got, h)
	}
	if got := h.Get("Evil"); got != "" {
		t.Fatalf("injected header Evil present: %q", got)
	}
	if got := h.Get("X-Extra-Per-Open"); got != "1" {
		t.Fatalf("X-Extra-Per-Open = %q", got)
	}
}

// --- Bug 7: event noise ---

func TestBug7NoCoreIdleObservation(t *testing.T) {
	ids := map[uint64]bool{}
	for _, o := range observedProperties {
		if o.name == "core-idle" {
			t.Fatal("core-idle is still observed")
		}
		if ids[o.id] {
			t.Fatalf("duplicate observe id %d (%s)", o.id, o.name)
		}
		ids[o.id] = true
	}
}

func TestBug7TimePosNotifyThrottled(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	clip := genClip(t, ffmpeg, "60fps.mkv", clipSpec{dur: 6, fps: 60})
	var mu sync.Mutex
	var stamps []time.Time
	var lastNotified time.Duration
	var pp atomic.Pointer[Player]
	p := newTestPlayer(t, Options{OnStateChange: func() {
		mu.Lock()
		stamps = append(stamps, time.Now())
		if p := pp.Load(); p != nil {
			lastNotified = p.Snapshot().Position
		}
		mu.Unlock()
	}})
	pp.Store(p)
	if err := p.Open(clip); err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, 8*time.Second, "steady playback", func() bool { return p.Snapshot().Position > 600*time.Millisecond })

	const window = 1500 * time.Millisecond
	start := time.Now()
	positions := map[time.Duration]bool{}
	for time.Since(start) < window {
		positions[p.Snapshot().Position] = true
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	n := 0
	for _, ts := range stamps {
		if !ts.Before(start) && ts.Before(start.Add(window)) {
			n++
		}
	}
	mu.Unlock()
	maxCalls := int(window/posNotifyInterval) + 4
	t.Logf("window %v: %d OnStateChange calls, %d distinct Snapshot positions", window, n, len(positions))
	if n > maxCalls {
		t.Fatalf("%d OnStateChange calls in %v, want <= %d (time-pos not throttled)", n, window, maxCalls)
	}
	if n < 5 {
		t.Fatalf("only %d OnStateChange calls in %v; position updates stalled", n, window)
	}
	if len(positions) < n+20 {
		t.Fatalf("Snapshot precision reduced: %d distinct positions vs %d notifications", len(positions), n)
	}

	// Seeks notify immediately with the new position.
	mu.Lock()
	before := len(stamps)
	mu.Unlock()
	seekAt := time.Now()
	if err := p.Seek(4 * time.Second); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	waitFor(t, 2*time.Second, "post-seek notification", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(stamps) > before && lastNotified >= 3900*time.Millisecond
	})
	t.Logf("post-seek notification after %v", time.Since(seekAt))

	// The trailing throttled position is flushed: after pausing, the last
	// notified position matches the final Snapshot position.
	if err := p.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitFor(t, 2*time.Second, "paused", func() bool { return p.Snapshot().State == StatePaused })
	time.Sleep(400 * time.Millisecond)
	final := p.Snapshot().Position
	mu.Lock()
	got := lastNotified
	mu.Unlock()
	if got != final {
		t.Fatalf("last notified position %v != final position %v (trailing update dropped)", got, final)
	}
}

// --- Bug 9: example races / labels and View documentation ---

func TestBug9ExampleAndViewDocs(t *testing.T) {
	// The example keeps its state on the UI thread: goroutines (file
	// dialogs) may only hand results over with a.post, never touch a.<field>
	// themselves, and on-screen shortcut hints follow the platform.
	files, err := filepath.Glob(filepath.Join("examples", "player", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("example sources: %v %v", files, err)
	}
	fset := token.NewFileSet()
	var all strings.Builder
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(src)
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			g, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			ast.Inspect(g.Call.Fun, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "post" {
						return false // runs on the UI thread
					}
				case *ast.FuncLit:
					if x != g.Call.Fun {
						return false // handed to someone else (a.post, dialogs)
					}
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "a" && x.Sel.Name != "win" && x.Sel.Name != "post" && x.Sel.Name != "subPos" && x.Sel.Name != "p" {
						t.Errorf("%s: goroutine touches UI state a.%s directly", fset.Position(x.Pos()), x.Sel.Name)
					}
				}
				return true
			})
			return false
		})
	}
	for _, bad := range []string{`"Open Video (Cmd+O)"`, `"MyGo Player (libmpv Zero-Copy)"`, `"Auto Copy (Windows / Copy-Back)"`, `"Cmd+O"`} {
		if strings.Contains(all.String(), bad) {
			t.Errorf("example still hardcodes macOS-only label %s", bad)
		}
	}
	view, err := os.ReadFile("view.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SetPanscan(1.0)", "Fit(ui.Cover)", "black bars"} {
		if !strings.Contains(string(view), want) {
			t.Errorf("view.go documentation missing %q", want)
		}
	}
}
