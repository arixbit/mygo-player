package player

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ffi"
	"github.com/egoist/mygo/ui"
)

func TestManifestValid(t *testing.T) {
	lib := ffi.New("mpv", pluginManifest)
	if lib.Name() != "mpv" {
		t.Fatalf("lib.Name() = %q, want mpv", lib.Name())
	}
	// Ensure manifest parsed cleanly without reporting a manifest syntax error.
	if _, err := lib.Path(); err != nil {
		// In environments without a local/published binary, Path may report
		// missing build, which is expected; it must not report invalid manifest.
		if got := err.Error(); len(got) > 0 && (contains(got, "invalid") || contains(got, "names no library")) {
			t.Fatalf("embedded mygo-plugin.json failed validation: %v", err)
		}
	}
}

func TestSplitHTTPHeaders(t *testing.T) {
	fields, ua, ref := splitHTTPHeaders(map[string]string{
		"Authorization": "Bearer abc,def",
		"X-Trace":       "123\r\nInjected: bad",
		"User-Agent":    "HeaderUA/1.0",
		"Referer":       "https://header.example/",
	}, "", "")

	if ua != "HeaderUA/1.0" {
		t.Fatalf("ua = %q, want HeaderUA/1.0", ua)
	}
	if ref != "https://header.example/" {
		t.Fatalf("ref = %q, want https://header.example/", ref)
	}
	want := []string{
		"Authorization: Bearer abc,def",
		"X-Trace: 123Injected: bad",
	}
	if !slices.Equal(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}

	// Explicit UA/Referrer take precedence over map entries.
	_, ua2, ref2 := splitHTTPHeaders(map[string]string{
		"User-Agent": "HeaderUA/1.0",
		"Referer":    "https://header.example/",
	}, "ExplicitUA/2.0", "https://explicit.example/")
	if ua2 != "ExplicitUA/2.0" || ref2 != "https://explicit.example/" {
		t.Fatalf("explicit precedence failed: ua=%q ref=%q", ua2, ref2)
	}
}

func TestParseTrackList(t *testing.T) {
	raw := []any{
		map[string]any{
			"id": int64(1), "type": "video", "codec": "h264",
			"selected": true, "demux-w": int64(1920), "demux-h": int64(1080), "demux-fps": 23.976,
		},
		map[string]any{
			"id": int64(1), "type": "audio", "title": "English", "lang": "eng",
			"codec": "aac", "selected": true, "default": true, "demux-channel-count": int64(2),
		},
		map[string]any{
			"id": int64(2), "type": "audio", "title": "Japanese", "lang": "jpn",
			"codec": "aac", "selected": false,
		},
		map[string]any{
			"id": int64(1), "type": "sub", "title": "Chinese", "lang": "zho",
			"codec": "subrip", "selected": true, "external": true, "external-filename": "/tmp/a.srt",
		},
	}

	tracks := parseTrackList(raw)
	if len(tracks) != 4 {
		t.Fatalf("len(tracks) = %d, want 4", len(tracks))
	}
	audios := filterTracks(tracks, TrackAudio)
	if len(audios) != 2 || audios[0].Lang != "eng" || audios[1].Lang != "jpn" {
		t.Fatalf("unexpected audio tracks: %+v", audios)
	}
	subs := filterTracks(tracks, TrackSub)
	if len(subs) != 1 || !subs[0].External || subs[0].ExternalFilename != "/tmp/a.srt" {
		t.Fatalf("unexpected subtitle tracks: %+v", subs)
	}
}

func requireLibMPVAndFFmpeg(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skipf("integration test not supported on %s", runtime.GOOS)
	}
	mustRun := os.Getenv("MYGO_REQUIRE_LIBMPV") == "1"
	if err := Load(); err != nil {
		if mustRun {
			t.Fatalf("libmpv required (MYGO_REQUIRE_LIBMPV=1) but failed to load: %v", err)
		}
		t.Skipf("libmpv not available in this environment: %v", err)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		if mustRun {
			t.Fatalf("ffmpeg required (MYGO_REQUIRE_LIBMPV=1) but not found in PATH: %v", err)
		}
		t.Skip("ffmpeg not available to synthesize MKV fixture")
	}
	return ffmpeg
}

// TestIntegrationMKVPlaybackAndM2 exercises the full stack on macOS, Windows,
// and Linux against a real libmpv library and a generated multi-track MKV under
// a Chinese UTF-8 path:
//   - libmpv FFI loading, Chinese-path MKV playback, frame rendering into MyGo
//     (zero-copy IOSurface -> native.Texture on macOS; MPV_RENDER_API_TYPE_SW
//     double-buffered -> ui.Bitmap on Windows and Linux), pixel color & upright
//     Y-orientation verification, overlay compositing above View(), dynamic
//     resize while paused, and Seek/Pause/Play.
//   - Multi-audio track switching, embedded + external Chinese-path SRT
//     subtitle loading and switching, hardware decoding options, and custom
//     HTTP headers (plus isolation across Open calls) via httptest.Server.
func TestIntegrationMKVPlaybackAndM2(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)

	major, minor, err := ClientAPIVersion()
	if err != nil {
		t.Fatalf("ClientAPIVersion: %v", err)
	}
	t.Logf("loaded libmpv client API v%d.%d from %s (%s/%s)", major, minor, mustLibPath(t), runtime.GOOS, runtime.GOARCH)

	tmp := t.TempDir()
	mkvPath, extSubPath := generateTestMKV(t, ffmpeg, tmp)

	// On headless macOS CI VMs lacking AppleM2ScalerParavirtDriver, VideoToolbox
	// zero-copy intermittently fails with -12909; use software decoding initially
	// on macOS CI for deterministic pixel checks while still testing SetHWDec below.
	var initHWDec HWDecMode
	if runtime.GOOS == "darwin" && os.Getenv("CI") != "" {
		initHWDec = HWDecNone
	}

	p, err := New(Options{
		HWDec:       initHWDec,
		AudioOutput: "null",
		LogLevel:    "warn",
		OnLog: func(level, prefix, text string) {
			t.Logf("[mpv:%s:%s] %s", level, prefix, text)
		},
	})
	if err != nil {
		t.Fatalf("player.New: %v", err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Errorf("player.Close: %v", err)
		}
	}()

	if err := p.Open(mkvPath); err != nil {
		t.Fatalf("player.Open(mkv): %v", err)
	}

	// Wait until video renders at least one frame.
	waitFor(t, 10*time.Second, "first rendered video frame", func() bool {
		w, h := p.FrameSize()
		return p.FramesRendered() > 0 && w > 0 && h > 0 && p.Snapshot().Duration > 0
	})

	snap := p.Snapshot()
	fw, fh := p.FrameSize()
	t.Logf("MKV playing (%s): video=%dx%d frame=%dx%d duration=%v frames=%d hwdec=%q",
		runtime.GOOS, snap.VideoWidth, snap.VideoHeight, fw, fh, snap.Duration, p.FramesRendered(), snap.CurrentHWDec)

	// Verify upright Y-orientation, RGB channels, and MyGo overlay compositing
	// above View() (confirming no native child window occlusion).
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			View(c, p).Fill()
			// Centered green overlay box must composite on top of the video.
			ui.Box(c).Absolute().Left(150).Top(110).Width(20).Height(20).Background(ui.RGB(0, 255, 0))
		})
	}, 320, 240)
	// Poll briefly so async GPU flushes (e.g. paravirtualized macOS CI VMs) and
	// initial hardware-decoder fallback settle before asserting exact pixels.
	waitFor(t, 5*time.Second, "rendered RED/BLUE/overlay pixels via View()", func() bool {
		tt.Frame()
		img := tt.Image()
		if img == nil || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 240 {
			return false
		}
		topC := img.RGBAAt(160, 50)
		botC := img.RGBAAt(160, 190)
		midC := img.RGBAAt(160, 120)
		return topC.R >= 180 && topC.G <= 60 && topC.B <= 60 &&
			botC.B >= 180 && botC.R <= 60 && botC.G <= 60 &&
			midC.G >= 200 && midC.R <= 40 && midC.B <= 40
	})
	rendered := tt.Image()
	// Fixture has top half RED (#FF0000) and bottom half BLUE (#0000FF).
	topC := rendered.RGBAAt(160, 50)
	botC := rendered.RGBAAt(160, 190)
	midC := rendered.RGBAAt(160, 120)
	t.Logf("MyGo ui.View rendered 320x240: top=(%d,%d,%d,%d) bottom=(%d,%d,%d,%d) overlay=(%d,%d,%d,%d)",
		topC.R, topC.G, topC.B, topC.A, botC.R, botC.G, botC.B, botC.A, midC.R, midC.G, midC.B, midC.A)

	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if p.Texture() != nil {
			t.Fatalf("expected nil Texture() on %s, got %v", runtime.GOOS, p.Texture())
		}
		if _, ok := p.ImageSource().(*ui.Bitmap); !ok {
			t.Fatalf("expected *ui.Bitmap ImageSource() on %s, got %T", runtime.GOOS, p.ImageSource())
		}
	}

	// Verify Pause, dynamic SetRenderSize while paused, and Seek.
	if err := p.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitFor(t, 4*time.Second, "paused state", func() bool { return p.Snapshot().Paused })

	p.SetRenderSize(640, 480)
	waitFor(t, 4*time.Second, "resized frame 640x480 while paused", func() bool {
		w, h := p.FrameSize()
		return w == 640 && h == 480
	})

	if err := p.Seek(1 * time.Second); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	waitFor(t, 4*time.Second, "seek position >= 0.8s", func() bool {
		return p.Snapshot().Position >= 800*time.Millisecond
	})

	// Verify hardware decoding mode switching.
	if hw := p.CurrentHWDec(); hw != "" && hw != "no" {
		t.Logf("hardware decoding active: %s", hw)
	} else {
		t.Logf("note: hwdec-current=%q (expected on headless VM without GPU decoder)", hw)
	}
	if err := p.SetHWDec(HWDecNone); err != nil {
		t.Fatalf("SetHWDec(no): %v", err)
	}
	switch runtime.GOOS {
	case "windows":
		if err := p.SetHWDec(HWDecD3D11VACopy); err != nil {
			t.Fatalf("SetHWDec(d3d11va-copy): %v", err)
		}
		if err := p.SetHWDec(HWDecAutoCopy); err != nil {
			t.Fatalf("SetHWDec(auto-copy): %v", err)
		}
	case "linux":
		// Verify copy-back auto mode on Linux without requiring VA-API hardware on CI runners.
		if err := p.SetHWDec(HWDecAutoCopy); err != nil {
			t.Fatalf("SetHWDec(auto-copy): %v", err)
		}
	default:
		if err := p.SetHWDec(HWDecAutoSafe); err != nil {
			t.Fatalf("SetHWDec(auto-safe): %v", err)
		}
	}

	// Verify multi-audio track detection & switching.
	waitFor(t, 4*time.Second, "2 audio tracks detected", func() bool {
		return len(p.AudioTracks()) == 2
	})
	audios := p.AudioTracks()
	t.Logf("audio tracks: %+v", audios)
	if err := p.SelectAudioTrack(audios[1].ID); err != nil {
		t.Fatalf("SelectAudioTrack(%d): %v", audios[1].ID, err)
	}
	waitFor(t, 4*time.Second, "audio track 2 selected", func() bool {
		return p.Snapshot().AudioTrackID == audios[1].ID
	})
	if err := p.DisableAudioTrack(); err != nil {
		t.Fatalf("DisableAudioTrack: %v", err)
	}
	waitFor(t, 4*time.Second, "audio disabled", func() bool {
		return p.Snapshot().AudioTrackID == 0
	})

	// Verify embedded subtitle + adding & switching external Chinese-path SRT subtitle + visibility toggle.
	waitFor(t, 4*time.Second, "embedded subtitle track detected", func() bool {
		return len(p.SubtitleTracks()) >= 1
	})
	if err := p.AddSubtitleFile(extSubPath, SubtitleSelect); err != nil {
		t.Fatalf("AddSubtitleFile(%s): %v", extSubPath, err)
	}
	waitFor(t, 4*time.Second, "external subtitle added and selected", func() bool {
		for _, tr := range p.SubtitleTracks() {
			if tr.External && tr.Selected {
				return true
			}
		}
		return false
	})
	subs := p.SubtitleTracks()
	t.Logf("subtitle tracks: %+v", subs)

	if err := p.SetSubtitleVisible(false); err != nil {
		t.Fatalf("SetSubtitleVisible(false): %v", err)
	}
	waitFor(t, 4*time.Second, "sub-visibility=false", func() bool { return !p.Snapshot().SubVisible })
	if err := p.SetSubtitleVisible(true); err != nil {
		t.Fatalf("SetSubtitleVisible(true): %v", err)
	}
	waitFor(t, 4*time.Second, "sub-visibility=true", func() bool { return p.Snapshot().SubVisible })

	if err := p.SelectSubtitleTrack(subs[0].ID); err != nil {
		t.Fatalf("SelectSubtitleTrack: %v", err)
	}
	waitFor(t, 4*time.Second, "embedded subtitle selected", func() bool {
		return p.Snapshot().SubTrackID == subs[0].ID
	})
	if err := p.DisableSubtitleTrack(); err != nil {
		t.Fatalf("DisableSubtitleTrack: %v", err)
	}
	waitFor(t, 4*time.Second, "subtitles disabled", func() bool {
		return p.Snapshot().SubTrackID == 0
	})

	// Verify custom HTTP Headers, User-Agent, and Referer end-to-end AND
	// confirm per-open headers do not leak to subsequent Open calls.
	var hdrMu sync.Mutex
	type reqHeaders struct {
		auth, custom, ua, ref string
	}
	byPath := map[string]reqHeaders{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdrMu.Lock()
		if _, seen := byPath[r.URL.Path]; !seen {
			byPath[r.URL.Path] = reqHeaders{
				auth:   r.Header.Get("Authorization"),
				custom: r.Header.Get("X-MyGo-Test"),
				ua:     r.Header.Get("User-Agent"),
				ref:    r.Header.Get("Referer"),
			}
		}
		hdrMu.Unlock()
		http.ServeFile(w, r, mkvPath)
	}))
	defer srv.Close()

	if err := p.Open(srv.URL+"/first.mkv", OpenOptions{
		Headers: map[string]string{
			"Authorization": "Bearer test-token,with,commas",
			"X-MyGo-Test":   "mygo-player-v02",
		},
		UserAgent: "MyGoPlayerTest/1.0",
		Referrer:  "https://ref.mygo.dev/watch",
	}); err != nil {
		t.Fatalf("Open(http first): %v", err)
	}
	waitFor(t, 8*time.Second, "HTTP request /first.mkv", func() bool {
		hdrMu.Lock()
		defer hdrMu.Unlock()
		_, ok := byPath["/first.mkv"]
		return ok
	})

	hdrMu.Lock()
	h1 := byPath["/first.mkv"]
	hdrMu.Unlock()
	t.Logf("HTTP /first.mkv headers: %+v", h1)
	if h1.auth != "Bearer test-token,with,commas" || h1.custom != "mygo-player-v02" ||
		h1.ua != "MyGoPlayerTest/1.0" || h1.ref != "https://ref.mygo.dev/watch" {
		t.Fatalf("unexpected /first.mkv headers: %+v", h1)
	}

	// Second Open without custom headers must not leak /first.mkv credentials.
	if err := p.Open(srv.URL + "/second.mkv"); err != nil {
		t.Fatalf("Open(http second): %v", err)
	}
	waitFor(t, 8*time.Second, "HTTP request /second.mkv", func() bool {
		hdrMu.Lock()
		defer hdrMu.Unlock()
		_, ok := byPath["/second.mkv"]
		return ok
	})

	hdrMu.Lock()
	h2 := byPath["/second.mkv"]
	hdrMu.Unlock()
	t.Logf("HTTP /second.mkv headers (after reset): %+v", h2)
	if h2.auth != "" || h2.custom != "" || h2.ref != "" {
		t.Fatalf("per-open HTTP headers leaked to subsequent Open: %+v", h2)
	}
}

// TestIntegrationLifecycleOpenClose exercises repeated Player creation,
// playback, frame rendering, and teardown (including rapid close before first
// frame and idempotent double Close), validating the singleton callback router
// and clean resource teardown across lifetimes.
func TestIntegrationLifecycleOpenClose(t *testing.T) {
	ffmpeg := requireLibMPVAndFFmpeg(t)
	tmp := t.TempDir()
	mkvPath, _ := generateTestMKV(t, ffmpeg, tmp)

	const cycles = 4
	for i := range cycles {
		p, err := New(Options{
			// Use software decoding in repeated lifecycle stress cycles so
			// headless CI VMs with limited/paravirtualized hardware decoder
			// sessions (e.g. macOS CI VideoToolbox -12909) do not flake.
			// Hardware decoding is exercised by TestIntegrationMKVPlaybackAndM2.
			HWDec:       HWDecNone,
			AudioOutput: "null",
			LogLevel:    "warn",
			OnLog: func(level, prefix, text string) {
				t.Logf("[cycle %d mpv:%s:%s] %s", i, level, prefix, text)
			},
		})
		if err != nil {
			t.Fatalf("cycle %d: New: %v", i, err)
		}
		t.Cleanup(func() { _ = p.Close() })
		if err := p.Open(mkvPath); err != nil {
			t.Fatalf("cycle %d: Open: %v", i, err)
		}
		waitFor(t, 8*time.Second, fmt.Sprintf("cycle %d first frame", i), func() bool {
			w, h := p.FrameSize()
			return p.FramesRendered() > 0 && w > 0 && h > 0
		})
		tt := ui.NewTester(func(c *ui.Context) {
			View(c, p).Fill()
		}, 160, 120)
		var lastTop [4]uint8
		deadline := time.Now().Add(5 * time.Second)
		matched := false
		for time.Now().Before(deadline) {
			tt.Frame()
			topC := tt.Image().RGBAAt(80, 30)
			lastTop = [4]uint8{topC.R, topC.G, topC.B, topC.A}
			if topC.R >= 180 && topC.G <= 60 && topC.B <= 60 {
				matched = true
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if !matched {
			w, h := p.FrameSize()
			t.Fatalf("cycle %d: timed out waiting for top RED pixel (lastTop=%v frames=%d size=%dx%d state=%s)",
				i, lastTop, p.FramesRendered(), w, h, p.Snapshot().State)
		}

		if err := p.Close(); err != nil {
			t.Fatalf("cycle %d: Close: %v", i, err)
		}
		// Second Close must be a safe no-op.
		if err := p.Close(); err != nil {
			t.Fatalf("cycle %d: second Close: %v", i, err)
		}
		if err := p.Play(); !errors.Is(err, ErrClosed) {
			t.Fatalf("cycle %d: Play after Close = %v, want ErrClosed", i, err)
		}
		if err := p.Open(mkvPath); !errors.Is(err, ErrClosed) {
			t.Fatalf("cycle %d: Open after Close = %v, want ErrClosed", i, err)
		}
	}

	// Rapid open -> immediate close cycles to catch shutdown races.
	for i := range 2 {
		p, err := New(Options{AudioOutput: "null"})
		if err != nil {
			t.Fatalf("rapid cycle %d: New: %v", i, err)
		}
		_ = p.Open(mkvPath)
		if err := p.Close(); err != nil {
			t.Fatalf("rapid cycle %d: Close: %v", i, err)
		}
	}
}

func generateTestMKV(t *testing.T, ffmpeg, dir string) (mkvPath, extSubPath string) {
	t.Helper()
	zhDir := filepath.Join(dir, "测试中文目录")
	if err := os.MkdirAll(zhDir, 0o755); err != nil {
		t.Fatal(err)
	}
	embSub := filepath.Join(zhDir, "内嵌字幕.srt")
	extSub := filepath.Join(zhDir, "外挂中文字幕.srt")
	mkvPath = filepath.Join(zhDir, "中文测试视频.mkv")

	sub1 := "1\n00:00:00,000 --> 00:00:03,000\nEmbedded English Subtitle\n"
	sub2 := "1\n00:00:00,000 --> 00:00:03,000\n外挂中文字幕测试\n"
	if err := os.WriteFile(embSub, []byte(sub1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extSub, []byte(sub2), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(ffmpeg,
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=320x120:r=24:d=3",
		"-f", "lavfi", "-i", "color=c=blue:s=320x120:r=24:d=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=3",
		"-i", embSub,
		"-filter_complex", "[0:v][1:v]vstack=inputs=2[v]",
		"-map", "[v]", "-map", "2:a", "-map", "3:a", "-map", "4:s",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-tune", "stillimage",
		"-c:a", "aac", "-b:a", "64k",
		"-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=English 440Hz",
		"-metadata:s:a:1", "language=jpn", "-metadata:s:a:1", "title=Japanese 880Hz",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "title=Embedded ENG",
		mkvPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate fixture MKV failed: %v\n%s", err, out)
	}
	return mkvPath, extSub
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", timeout, desc)
}

func mustLibPath(t *testing.T) string {
	t.Helper()
	p, err := LibraryPath()
	if err != nil {
		return fmt.Sprintf("<err:%v>", err)
	}
	return p
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
