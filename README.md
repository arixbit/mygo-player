English | [中文文档](README_ZH.md)

# mygo-player

Standalone native UI video player plugin for [MyGo](https://github.com/egoist/mygo) (`v0.10.0`), powered by **libmpv** (LGPL 2.1+) with **zero cgo** (`CGO_ENABLED=0`).

`mygo-player` follows the same plugin layout as official MyGo native plugins (`mygo-plugin.json`, `mygo/ffi`, `Load`, `LibraryPath`, `New`, `View`). Frames render directly inside MyGo's native UI scene graph—never as a separate native child window—so HUD controls, danmaku, rounded corners, and popovers always composite cleanly above the video.

## Why `mygo-player` vs Built-in `ui/video`?

MyGo's built-in `ui/video` wraps each OS's media framework (AVFoundation, Media Foundation, GStreamer, and browser `<video>`). It requires no extra native library and is ideal for simple MP4/H.264 clips or web builds. `mygo-player` complements `ui/video` when your desktop app needs full media-player features:

| Capability | Built-in `ui/video` | `mygo-player` |
| --- | --- | --- |
| Media engine | OS media frameworks (AVFoundation / Media Foundation / GStreamer / `<video>`) | Embedded `libmpv` (LGPL 2.1+) via `mygo/ffi`, zero cgo |
| Extra native library | None | `libmpv` shared library (auto-managed via `mygo-plugin.json`) |
| Formats & codecs | MP4 / MOV (H.264 / AAC) everywhere; HEVC / VP9 / AV1 depend on OS decoders | Full format playback out of the box (MKV, MP4, WebM, TS, FLV; H.264, HEVC, AV1, VP9, etc.) |
| Multi-audio tracks | Not exposed | List, switch, and disable audio tracks at runtime |
| Subtitles | Not exposed | Embedded subtitles + external subtitle files (SRT, ASS/SSA, VTT, SUP) with libass styling & toggle |
| Online streams & auth | Plain URLs via `OpenURL` | Custom HTTP headers per stream (auth tokens, cookies, anti-hotlink `Referer` & `User-Agent`), isolated per `Open()` |
| Hardware decoding control | Managed automatically by OS | Configurable & switchable at runtime (VideoToolbox, D3D11VA, VA-API, or software) |
| UI overlay compositing | Scene-graph composited | Scene-graph composited (HUD controls, danmaku, popovers, rounded corners stay above video) |
| Best fit | Trailers, onboarding clips, lightweight embeds, browser/WASM apps | Media players, anime/movie apps, NAS/Emby/Jellyfin clients, authenticated stream players |

## Platform Support

| Platform | Rendering Path | Default HWDec | Audio Output | Prebuilt `mygo-plugin.json` / Build |
| --- | --- | --- | --- | --- |
| `darwin-arm64` | Zero-copy GPU (`IOSurface` -> Metal `*native.Texture`) | `auto-safe` (VideoToolbox) | CoreAudio | Published (`scripts/build-libmpv-macos.sh`) |
| `darwin-amd64` | Zero-copy GPU (`IOSurface` -> Metal `*native.Texture`) | `auto-safe` (VideoToolbox) | CoreAudio | Published (`scripts/build-libmpv-macos.sh`) |
| `windows-amd64` | Double-buffered `*ui.Bitmap` (`MPV_RENDER_API_TYPE_SW`, no child HWND occlusion) | `auto-copy` (D3D11VA) | WASAPI | Published (`scripts/build-libmpv-windows.sh`) |
| `windows-arm64` | Double-buffered `*ui.Bitmap` (`MPV_RENDER_API_TYPE_SW`, no child HWND occlusion) | `auto-copy` (D3D11VA) | WASAPI | Published (`scripts/build-libmpv-windows.sh`) |
| `linux-amd64` | Double-buffered `*ui.Bitmap` (`MPV_RENDER_API_TYPE_SW`) | `auto-copy` (VA-API / SW fallback) | PulseAudio (incl. `pipewire-pulse`) / ALSA | Published (`scripts/build-libmpv-linux.sh`) |
| `linux-arm64` | Double-buffered `*ui.Bitmap` (`MPV_RENDER_API_TYPE_SW`) | `auto-copy` (copy-back / SW fallback) | PulseAudio (incl. `pipewire-pulse`) / ALSA | Published (`scripts/build-libmpv-linux.sh`) |

> **Unsupported targets (`js/wasm`, iOS, Android):** `mygo-player` compiles cleanly without desktop `purego`/renderer dependencies (`CGO_ENABLED=0` for `js/wasm`) and returns `errors.ErrUnsupported` at runtime, so cross-platform codebases can import it safely.

## Installation

```bash
go get github.com/arixbit/mygo-player
```

- Requires **Go** and **MyGo (`v0.10.0`)** with **`CGO_ENABLED=0`**.
- `mygo-plugin.json` is embedded in the package, so `mygo dev` and `mygo build` automatically discover and bundle `libmpv`.

## Quick Start

Leave `HWDec` empty to use the recommended platform default automatically (`auto-safe` on macOS, `auto-copy` on Windows and Linux):

```go
package main

import (
	"log"

	player "github.com/arixbit/mygo-player"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		p, err := player.New(player.Options{})
		if err != nil {
			log.Fatal(err)
		}
		_ = p.Open("/path/to/movie.mkv", player.OpenOptions{
			Subtitles: []string{"/path/to/movie.srt"},
		})

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "Player",
			Width:  1024,
			Height: 600,
			Content: ui.View(func(c *ui.Context) {
				player.View(c, p).Fill()
			}),
		})
		win.OnClosed(func() { _ = p.Close() })
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

### Running the Example Player

```bash
# Uses the published libmpv (via mygo-plugin.json) or a locally installed libmpv:
CGO_ENABLED=0 go run ./examples/player /path/to/video.mkv

# Or point to a specific libmpv shared library (.dylib / .dll / .so):
MYGO_NATIVE_MPV=/path/to/libmpv.dylib CGO_ENABLED=0 go run ./examples/player \
  -sub /path/to/subtitle.srt \
  /path/to/video.mkv
```

The example is a complete, minimal player: drop a video (or choose one with `Cmd+O` / `Ctrl+O`, or "Open With" from Finder/Explorer) and it plays. Matching subtitle files next to the video load automatically, and dropping subtitle files on a playing video adds them. The control bar hides with the cursor after a moment of playback and comes back on any movement; subtitles move up out of its way while it shows. Menus only appear when they offer a choice (the audio menu needs two or more tracks), narrow windows fold what doesn't fit into the More (⋯) menu, tracks are named in plain language ("Japanese · Commentary"), errors show as toasts or as a card with a way forward, and every transition respects the system's Reduce Motion setting.

Keyboard shortcuts in `examples/player` (shown in each control's tooltip):
- `Space` / `K`: Play / Pause (replays at the end); clicking the picture plays or pauses immediately
- `Left` / `Right`: Back / forward 5 seconds
- `Up` / `Down`: Volume up / down (turning it up unmutes)
- `M`: Mute / Unmute
- `C`: Subtitles off / back on
- `F`, or a double click on the picture: Full screen (`Esc` leaves it; undoes the first click's pause/play)
- `Cmd+O` (macOS) / `Ctrl+O` (Windows & Linux): Choose a video

## Audio Tracks, Subtitles & Online Streams (Custom Headers)

- **Online streams with custom HTTP headers:** Pass auth tokens (`Authorization`), session `Cookie`s, and anti-hotlink `Referer` / `User-Agent` headers per stream. Per-open headers are isolated and automatically reset on the next `Open()` call so credentials never leak between videos.
- **Multi-audio tracks:** Inspect all embedded audio streams (`AudioTracks()`), switch languages/commentary tracks on the fly (`SelectAudioTrack`), or mute audio decoding (`DisableAudioTrack`).
- **Readable track names:** `Track.DisplayName()` names a track for menus ("Japanese · Commentary", "English · Forced", or an external subtitle's file name), and `LanguageName("jpn")` spells out the ISO 639-1/639-2 codes of about fifty common languages (other codes stay as upper-case codes; `und`, `mul`, `mis` and `zxx`, which name no single language, return ""), so apps don't show raw `#2 [jpn] (aac)` labels.
- **Embedded & external subtitles:** Switch between embedded tracks or attach external subtitle files (`.srt`, `.ass`, `.ssa`, `.vtt`, `.sup`) with `AddSubtitleFile`, and show/hide them anytime (`SetSubtitleVisible`).
- **Mute toggle:** `ToggleMute()` mutes or unmutes; unmuting at volume 0 restores an audible volume so it is never silent.
- **Runtime hardware decoding:** Switch modes dynamically with `SetHWDec` (`HWDecAutoSafe`, `HWDecAutoCopy`, `HWDecVideoToolbox`, `HWDecD3D11VACopy`, `HWDecVAAPICopy`, `HWDecNone`) and inspect the active decoder with `CurrentHWDec()`.

```go
// Play an online stream with auth token, cookie, and anti-hotlink Referer & User-Agent.
err := p.Open("https://media.example.com/episode.mkv", player.OpenOptions{
	Headers: map[string]string{
		"Authorization": "Bearer your-token",
		"Cookie":        "session=abc",
	},
	UserAgent: "MyApp/1.0",
	Referrer:  "https://media.example.com/",
})

// Switch audio tracks.
if audios := p.AudioTracks(); len(audios) > 1 {
	_ = p.SelectAudioTrack(audios[1].ID)
}

// Load an external subtitle (SRT/ASS/VTT/SUP) and toggle visibility.
_ = p.AddSubtitleFile("/path/to/subtitle.ass", player.SubtitleSelect)
_ = p.SetSubtitleVisible(true)

// Query or switch hardware decoding at runtime.
_ = p.SetHWDec(player.HWDecAutoCopy)
log.Println("Active hwdec:", p.CurrentHWDec())
```

## Audio/Video & Subtitle Fine-Tuning

All tuning methods return `ErrClosed` after `Close`, reject out-of-range values with an error, redraw the current frame when the picture changes, and are mirrored in `Snapshot()` (also when changed via `SetPropertyString`):

| Method | Range | Snapshot field |
| --- | --- | --- |
| `SetSubtitleDelay(d time.Duration)` / `SetAudioDelay(d time.Duration)` | any (positive = later) | `SubDelay` / `AudioDelay` |
| `SetSubtitleScale(f float64)` | `(0, 100]`, default `1.0` | `SubScale` |
| `SetSubtitlePos(p int)` | `0..150` (% of height), default `100` | `SubPos` |
| `SetVideoRotate(deg int)` | `0`, `90`, `180`, `270` | `VideoRotate` |
| `SetPanscan(f float64)` | `0..1` (`1` = crop to fill the View) | `Panscan` |
| `FrameStep()` / `FrameBackStep()` | pause and step one frame forward / back | `Position` |

```go
_ = p.SetSubtitleDelay(250 * time.Millisecond)
_ = p.SetSubtitleScale(1.2)
_ = p.SetPanscan(1.0) // fill a View whose aspect differs from the video (no black bars)
```

> `View` always renders at the element's full size (libmpv letterboxes inside it, keeping subtitles sharp), so `Fit(ui.Cover)` does not remove black bars — use `SetPanscan`.

Playback notes: `TogglePause`/`Play` at EOF replay from the start; with the default `KeepOpen`, `OnEndFile(nil)` fires once when EOF is reached; `Close` may be called from inside `OnStateChange`/`OnEndFile`; position-only `OnStateChange` notifications are throttled to ~100 ms while `Snapshot().Position` keeps full precision.

## Headless UI Testing (`ui.NewTester`)

Because `player.View` renders straight into MyGo's scene graph, you can test player layouts, HUD overlays, empty/idle states, and HiDPI viewport sizing deterministically with MyGo's headless `ui.NewTester`—without opening a window or even installing `libmpv`:

```go
func TestPlayerOverlay(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			player.View(c, nil).Fill()
			ui.Text(c, "Paused").Absolute().Left(16).Top(16)
		})
	}, 320, 180)
	tt.Frame()

	if !tt.HasText("Paused") {
		t.Fatal("expected overlay text to render above player.View")
	}
}
```

Run tests locally:

```bash
# Fast headless UI & unit tests (no libmpv required):
CGO_ENABLED=0 go test -v -run 'TestView|TestManifest|TestSplitHTTP|TestParseTrack|TestSWRenderer' ./...

# Full test suite including real MKV playback integration tests (requires libmpv + ffmpeg):
CGO_ENABLED=0 MYGO_REQUIRE_LIBMPV=1 go test -v ./...
```

## Native Library Lookup & LGPL Builds

`mygo-player` embeds `mygo-plugin.json` and resolves `libmpv` via `mygo/ffi` in this order:

1. `player.SetLibraryPath(...)` or the `$MYGO_NATIVE_MPV` environment variable.
2. Packaged app resources (`Contents/Resources/libmpv.dylib` / `Contents/Frameworks/` on macOS; `mpv-2.dll` next to the executable on Windows; `libmpv.so` next to or bundled with the executable on Linux).
3. Local `.mygo/native/<goos>-<goarch>/` directory (unpackaged runs).
4. Published SHA-256 verified binary declared in `mygo-plugin.json` (downloaded once into the user cache in unpackaged runs; bundled automatically by `mygo build`).
5. Local development fallbacks when unpackaged (Homebrew `/opt/homebrew/lib/libmpv.dylib` or `/usr/local/lib/libmpv.dylib` on macOS; local directory `mpv-2.dll` on Windows; system `libmpv.so` / `libmpv.so.2` paths on Linux).

To build self-contained LGPL-2.1+ libraries locally or via GitHub Actions:
- **macOS:** `./scripts/build-libmpv-macos.sh arm64 dist/libmpv-darwin-arm64.dylib`
- **Windows:** `./scripts/build-libmpv-windows.sh amd64 dist/mpv-2-windows-amd64.dll` (MSYS2 UCRT64) or `./scripts/build-libmpv-windows.sh arm64 dist/mpv-2-windows-arm64.dll` (MSYS2 CLANGARM64)
- **Linux:** `./scripts/build-libmpv-linux.sh amd64 dist/libmpv-linux-amd64.so`
- **GitHub Actions:** `.github/workflows/build-libmpv.yml` builds `darwin-arm64`, `darwin-amd64`, `windows-amd64`, `windows-arm64`, `linux-amd64`, and `linux-arm64` LGPL-2.1+ libraries, checks shared-library dependencies, runs zero-cgo Go integration tests, publishes SHA-256 assets to GitHub Releases, and updates `mygo-plugin.json`.

## License

- Go plugin source code, examples, and build scripts: **MIT License** ([LICENSE](LICENSE)).
- Underlying `libmpv` shared library: **LGPL 2.1 or later** (compiled strictly without GPL or nonfree components).

### Distributing apps with `mygo-player` (LGPL 2.1)

Because desktop apps using `mygo-player` bundle the LGPL 2.1+ `libmpv` shared library, apps you distribute (including closed-source or commercial apps) should:

1. **Keep `libmpv` as a separate, replaceable shared library** (`libmpv.dylib` on macOS, `mpv-2.dll` on Windows, `libmpv.so` on Linux) beside the app—this is already what `mygo build` does automatically—and avoid statically linking `libmpv` into your executable or blocking users from swapping it (or setting `MYGO_NATIVE_MPV`).
2. **Include the LGPL 2.1 license notice** in your app's license notices / distribution (stating that the app uses LGPL 2.1+ `libmpv` and its included third-party libraries, along with the license text and source reference).

See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for the complete list of third-party licenses, source references, and LGPL 2.1 Section 6 dynamic relinking details.
