# mygo-player

Standalone community video player plugin for [MyGo](https://github.com/egoist/mygo) (`v0.10.0`), powered by **libmpv** (LGPL 2.1+) with **zero cgo**, `mygo/ffi`, `mygo-plugin.json`, zero-copy GPU rendering on macOS (`IOSurface` → Metal `*native.Texture`), and double-buffered `ui.Bitmap` compositing on Windows (`MPV_RENDER_API_TYPE_SW` + `hwdec=auto-copy` + `ao=wasapi`, with no child HWND occlusion).

## Features

- **Zero cgo**: Pure Go bindings via `github.com/egoist/mygo/ffi` + `purego` (`CGO_ENABLED=0`) shared across macOS and Windows.
- **Zero-Copy GPU Rendering (macOS)**: Renders `libmpv` frames (`MPV_RENDER_API_TYPE_OPENGL`) via a pooled `CGL` + `IOSurface` (BGRA8Unorm) ring directly into MyGo's `*native.Texture` (`MTLTexture`).
- **Scene-Graph Composited Rendering (Windows `windows-amd64`)**: Renders `libmpv` frames (`MPV_RENDER_API_TYPE_SW` + `hwdec=auto-copy` / `d3d11va-copy` + `ao=wasapi`) at viewport physical size into a double-buffered `ui.Bitmap` (`UpdateYUV` NV12 preferred, `Update` RGBA fallback), keeping danmaku, translucent glass HUDs, rounded corners, and popovers cleanly above video without child HWND occlusion.
- **Hardware Decoding**: Apple VideoToolbox (`auto-safe` / `videotoolbox` / `videotoolbox-copy`), Windows D3D11VA (`auto-copy` / `d3d11va-copy`), or software (`no`), dynamically switchable at runtime.
- **Multi-Audio & Subtitles**: Inspect `AudioTracks()` / `SubtitleTracks()`, switch tracks on the fly, toggle visibility, and attach external subtitles (`AddSubtitleFile` for `.srt`, `.ass`, `.ssa`, `.vtt`, `.sup`).
- **Custom HTTP Headers**: Per-open and default HTTP headers (`Headers`, `UserAgent`, `Referrer`) passed via libmpv structured node arrays (`http-header-fields`) with automatic isolation between `Open()` calls.
- **MyGo Plugin Manifest (`mygo-plugin.json`)**: Compatible with `mygo dev`, `mygo build`, `MYGO_NATIVE_MPV`, and automated GitHub Actions LGPL-2.1+ builds.

## Installation

```bash
go get github.com/arixbit/mygo-player
```

## Quick Start

```go
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	player "github.com/arixbit/mygo-player"
)

func main() {
	mygo.App.WhenReady(func() {
		p, err := player.New(player.Options{
			HWDec: player.HWDecAutoSafe, // VideoToolbox on macOS
		})
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

### Custom HTTP Headers, Audio & Subtitles

```go
// Open a network stream with per-request HTTP headers (isolated per Open call).
err := p.Open("https://media.example.com/episode.mkv", player.OpenOptions{
	Headers: map[string]string{
		"Authorization": "Bearer your-token",
		"Cookie":        "session=abc",
	},
	UserAgent: "MyApp/1.0",
	Referrer:  "https://media.example.com/",
})

// Switch audio tracks & subtitles.
audios := p.AudioTracks()
_ = p.SelectAudioTrack(audios[1].ID)

_ = p.AddSubtitleFile("/path/to/chinese.ass", player.SubtitleSelect)
_ = p.SetSubtitleVisible(true)

// Query or change hardware decoding mode.
_ = p.SetHWDec(player.HWDecVideoToolbox)
log.Println("Active hwdec:", p.CurrentHWDec())
```

## Running the Example Player

```bash
# With a published libmpv (via mygo-plugin.json) or Homebrew mpv (`brew install mpv`):
CGO_ENABLED=0 go run ./examples/player /path/to/video.mkv

# Or explicitly specify a libmpv dylib:
MYGO_NATIVE_MPV=/path/to/libmpv.dylib CGO_ENABLED=0 go run ./examples/player \
  -hwdec auto-safe \
  -sub /path/to/subtitle.srt \
  /path/to/video.mkv
```

Keyboard shortcuts in `examples/player`:
- `Space`: Play / Pause
- `Left` / `Right`: Seek -5s / +5s
- `M`: Mute / Unmute
- `Cmd+O`: Open local video file

## Native Library Lookup & Releases

`mygo-player` embeds `mygo-plugin.json` and resolves `libmpv` via `mygo/ffi`:
1. `$MYGO_NATIVE_MPV` environment variable (or `player.SetLibraryPath(...)`).
2. Packaged app resources (`Contents/Resources/libmpv.dylib`) / `Contents/Frameworks/` / executable directory.
3. Local `.mygo/native/<goos>-<goarch>/libmpv.dylib` (unpackaged runs).
4. Published SHA-256 verified binary declared in `mygo-plugin.json` (downloaded once into user cache in unpackaged runs; bundled by `mygo build`).
5. Standard macOS Homebrew paths (`/opt/homebrew/lib/libmpv.dylib`, `/usr/local/lib/libmpv.dylib`) as an unpackaged dev fallback.

To build self-contained LGPL-2.1+ libraries locally or in GitHub Actions:
- macOS local script: `./scripts/build-libmpv-macos.sh arm64 dist/libmpv-darwin-arm64.dylib`
- Windows local script (MSYS2 UCRT64): `./scripts/build-libmpv-windows.sh amd64 dist/mpv-2.dll`
- GitHub Actions workflow: `.github/workflows/build-libmpv.yml` (builds `darwin-arm64`, `darwin-amd64`, and `windows-amd64`, verifies zero non-system dependencies via `otool -L` / `objdump -p`, runs zero-cgo Go integration tests, uploads SHA-256 assets to GitHub Release, and updates `mygo-plugin.json`).

## License

- Go plugin source code, examples, and scripts: **MIT License** ([LICENSE](LICENSE)).
- Underlying `libmpv` dynamic library: **LGPL 2.1 or later** (compiled strictly without GPL/nonfree components). See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for full details and dynamic relinking instructions.
