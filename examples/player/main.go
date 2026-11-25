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
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	// Files opened from Finder / Explorer ("Open With") play here too. The
	// listener must be in place before Run to receive the file that
	// launched the app, which arrives before the window exists.
	launch := &launchFiles{}
	mygo.App.OnOpenFile(launch.open)

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
		app.cmds = newCommandQueue()
		app.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     appTitle,
			Width:     1120,
			Height:    680,
			MinWidth:  320,
			MinHeight: 220,
			StateKey:  "player",
			Content:   ui.View(app.view),
		})
		app.win.OnClosed(func() {
			app.cmds.stop() // drop what waits
			// Close waits for a command libmpv may still be running (a slow
			// subtitle download): let it, off the UI thread.
			go func() { _ = p.Close() }()
		})

		// The file the app was launched with, from the command line or the
		// desktop, opens once: the system may pass it both ways, and before
		// or after the window is ready.
		var arg string
		if flag.NArg() > 0 {
			arg = flag.Arg(0)
		}
		paths := launch.attach(app, arg)
		if arg != "" {
			app.open(arg, subs)
		}
		if len(paths) > 0 {
			app.openPaths(paths, true)
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// launchFiles holds files the desktop opens with the app until its window
// is ready, then hands them to it. The file given on the command line is
// skipped once if the desktop passes it too.
type launchFiles struct {
	mu        sync.Mutex
	app       *demoApp
	queue     []string
	arg       string    // the command line's file
	skipUntil time.Time // the desktop's copies of arg are ignored until then
}

// launchEcho is how long after startup the desktop may still pass on the
// file the app was launched with.
const launchEcho = 3 * time.Second

func (l *launchFiles) open(path string) {
	l.mu.Lock()
	app := l.app
	switch {
	case app == nil:
		l.queue = append(l.queue, path)
	case l.arg != "" && samePath(path, l.arg) && time.Now().Before(l.skipUntil):
		app = nil // the launch file again, not a new request
	}
	l.mu.Unlock()
	if app != nil {
		app.post(func() { app.openPaths([]string{path}, true) })
	}
}

// attach hands later files to app and returns those that came before,
// without the command line's file arg.
func (l *launchFiles) attach(app *demoApp, arg string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.app, l.arg, l.skipUntil = app, arg, time.Now().Add(launchEcho)
	var q []string
	for _, p := range l.queue {
		if arg != "" && samePath(p, arg) {
			continue
		}
		q = append(q, p)
	}
	l.queue = nil
	return q
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}
