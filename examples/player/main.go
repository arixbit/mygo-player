// Example player demonstrates github.com/arixbit/mygo-player in a MyGo native
// UI window on macOS (zero-copy IOSurface -> Metal), Windows and Linux
// (software render API -> ui.Bitmap): MKV playback, multi-audio track
// switching, embedded & external subtitles, and custom HTTP headers, behind
// a control bar that stays out of the way of the picture.
//
// Usage:
//
//	go run ./examples/player /path/to/video.mkv
//	go run ./examples/player -hwdec videotoolbox -sub /path/to/sub.srt /path/to/video.mkv
//	go run ./examples/player -header "Authorization: Bearer token" -ua "MyPlayer/1.0" https://example.com/video.mkv
//
// Drop videos or subtitle files on the window, or open them with Cmd+O
// (Ctrl+O). Space plays and pauses, Left/Right skip 5 seconds, Up/Down change
// the volume, M mutes, C turns subtitles on and off, F enters full screen.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	player "github.com/arixbit/mygo-player"
)

type headerFlags map[string]string

func (h headerFlags) String() string { return fmt.Sprintf("%v", map[string]string(h)) }

func (h headerFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, ":")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("invalid header %q, expected 'Key: Value'", v)
	}
	h[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}

func main() {
	log.SetOutput(os.Stderr)

	headers := make(headerFlags)
	flag.Var(headers, "header", "Custom HTTP header 'Key: Value' (repeatable)")
	ua := flag.String("ua", "", "Custom HTTP User-Agent")
	ref := flag.String("referer", "", "Custom HTTP Referer")
	hwdec := flag.String("hwdec", "", "Hardware decoding mode (empty = platform default: auto-safe on macOS, auto-copy on Windows/Linux)")
	subPath := flag.String("sub", "", "External subtitle file (.srt, .ass, .vtt) to load with media")
	libPath := flag.String("libmpv", "", "Explicit path to libmpv dynamic library (optional, overrides MYGO_NATIVE_MPV)")
	logLevel := flag.String("log", "", "libmpv log level (e.g. warn, info, v)")
	flag.Parse()

	if *libPath != "" {
		player.SetLibraryPath(*libPath)
	}
	var subs []string
	if *subPath != "" {
		subs = append(subs, *subPath)
	}

	mygo.App.WhenReady(func() {
		p, err := player.New(player.Options{
			// Headers, User-Agent and Referer given on the command line
			// apply to every stream opened in this window.
			HWDec:       player.HWDecMode(*hwdec),
			HTTPHeaders: headers,
			UserAgent:   *ua,
			Referrer:    *ref,
			LogLevel:    *logLevel,
			OnLog: func(level, prefix, text string) {
				log.Printf("[mpv:%s:%s] %s", level, prefix, text)
			},
		})
		if err != nil {
			log.Fatalf("failed to create player: %v", err)
		}

		app := newDemoApp(p, func(c *ui.Context) ui.Element { return player.View(c, p) })
		app.startSubPosWorker()
		app.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     appTitle,
			Width:     1120,
			Height:    680,
			MinWidth:  320,
			MinHeight: 220,
			StateKey:  "player",
			Content:   ui.View(app.view),
		})
		app.win.OnClosed(func() { _ = p.Close() })
		// Files opened from Finder / Explorer ("Open With") play here too.
		mygo.App.OnOpenFile(func(path string) {
			app.post(func() { app.openPaths([]string{path}) })
		})
		if flag.NArg() > 0 {
			app.open(flag.Arg(0), subs)
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
