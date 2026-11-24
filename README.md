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

Keyboard shortcuts in `examples/player`:
- `Space`: Play / Pause
- `Left` / `Right`: Seek -5s / +5s
- `M`: Mute / Unmute
- `Cmd+O` (macOS) / `Ctrl+O` (Windows & Linux): Open local video file

## Audio Tracks, Subtitles & Online Streams (Custom Headers)

- **Online streams with custom HTTP headers:** Pass auth tokens (`Authorization`), session `Cookie`s, and anti-hotlink `Referer` / `User-Agent` headers per stream. Per-open headers are isolated and automatically reset on the next `Open()` call so credentials never leak between videos.
- **Multi-audio tracks:** Inspect all embedded audio streams (`AudioTracks()`), switch languages/commentary tracks on the fly (`SelectAudioTrack`), or mute audio decoding (`DisableAudioTrack`).
- **Embedded & external subtitles:** Switch between embedded tracks or attach external subtitle files (`.srt`, `.ass`, `.ssa`, `.vtt`, `.sup`) with `AddSubtitleFile`, and show/hide them anytime (`SetSubtitleVisible`).
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
- **Windows (MSYS2 UCRT64):** `./scripts/build-libmpv-windows.sh amd64 dist/mpv-2.dll`
- **Linux:** `./scripts/build-libmpv-linux.sh amd64 dist/libmpv-linux-amd64.so`
- **GitHub Actions:** `.github/workflows/build-libmpv.yml` builds `darwin-arm64`, `darwin-amd64`, `windows-amd64`, `linux-amd64`, and `linux-arm64` LGPL-2.1+ libraries, checks shared-library dependencies, runs zero-cgo Go integration tests, publishes SHA-256 assets to GitHub Releases, and updates `mygo-plugin.json`.

## License

- Go plugin source code, examples, and build scripts: **MIT License** ([LICENSE](LICENSE)).
- Underlying `libmpv` shared library: **LGPL 2.1 or later** (compiled strictly without GPL or nonfree components). See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for the complete list of third-party licenses and LGPL dynamic relinking instructions.
