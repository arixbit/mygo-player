// Example player demonstrates github.com/arixbit/mygo-player in a MyGo native
// UI window on macOS with zero-copy GPU rendering (IOSurface -> Metal), MKV
// playback, multi-audio track switching, embedded & external subtitles,
// hardware decoding (VideoToolbox), and custom HTTP headers.
//
// Usage:
//
//	go run ./examples/player /path/to/video.mkv
//	go run ./examples/player -hwdec videotoolbox -sub /path/to/sub.srt /path/to/video.mkv
//	go run ./examples/player -header "Authorization: Bearer token" -ua "MyPlayer/1.0" https://example.com/video.mkv
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

type demoApp struct {
	win       *mygo.Window
	p         *player.Player
	hwdecMode player.HWDecMode
	lastErr   string
	filePath  string

	initialTarget string
	initialOpts   player.OpenOptions
}

func main() {
	headers := make(headerFlags)
	flag.Var(headers, "header", "Custom HTTP header 'Key: Value' (repeatable)")
	ua := flag.String("ua", "", "Custom HTTP User-Agent")
	ref := flag.String("referer", "", "Custom HTTP Referer")
	hwdec := flag.String("hwdec", string(player.HWDecAutoSafe), "Hardware decoding mode: auto-safe, videotoolbox, videotoolbox-copy, no")
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
	var target string
	if flag.NArg() > 0 {
		target = flag.Arg(0)
	}

	mode := player.HWDecMode(*hwdec)
	p, err := player.New(player.Options{
		HWDec:       mode,
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
	defer p.Close()

	app := &demoApp{
		p:             p,
		hwdecMode:     mode,
		initialTarget: target,
		initialOpts: player.OpenOptions{
			Headers:   headers,
			UserAgent: *ua,
			Referrer:  *ref,
			Subtitles: subs,
		},
	}

	mygo.App.WhenReady(func() {
		app.win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "MyGo Player (libmpv Zero-Copy)",
			Width:   1120,
			Height:  680,
			Content: ui.View(app.view),
		})
		app.win.OnClose(func(*mygo.CloseEvent) {
			_ = app.p.Close()
		})
		if app.initialTarget != "" {
			app.openTarget(app.initialTarget, app.initialOpts)
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *demoApp) openTarget(target string, opts player.OpenOptions) {
	a.filePath = target
	a.lastErr = ""
	if err := a.p.Open(target, opts); err != nil {
		a.lastErr = err.Error()
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	if a.win != nil {
		a.win.SetTitle("MyGo Player — " + filepath.Base(target))
		a.win.Invalidate()
	}
}

func (a *demoApp) pickMediaFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: a.win,
			Title:  "Open Media File",
			Filters: []mygo.FileFilter{
				{Name: "Video Files", Extensions: []string{"mkv", "mp4", "mov", "webm", "avi", "ts", "m2ts", "flv", "m4v"}},
				{Name: "All Files", Extensions: []string{"*"}},
			},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.openTarget(paths[0], player.OpenOptions{})
	}()
}

func (a *demoApp) pickSubtitleFile() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: a.win,
			Title:  "Load External Subtitle",
			Filters: []mygo.FileFilter{
				{Name: "Subtitles", Extensions: []string{"srt", "ass", "ssa", "vtt", "sub", "sup"}},
			},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		if err := a.p.AddSubtitleFile(paths[0], player.SubtitleSelect); err != nil {
			a.lastErr = err.Error()
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	}()
}

func (a *demoApp) view(c *ui.Context) {
	snap := a.p.Snapshot()

	// Keyboard shortcuts.
	if c.Shortcut(0, ui.KeySpace) && !c.ShortcutRepeated() {
		_ = a.p.TogglePause()
	}
	if c.Shortcut(0, ui.KeyLeft) {
		_ = a.p.SeekRelative(-5 * time.Second)
	}
	if c.Shortcut(0, ui.KeyRight) {
		_ = a.p.SeekRelative(5 * time.Second)
	}
	if c.Shortcut(0, ui.KeyM) && !c.ShortcutRepeated() {
		_ = a.p.SetMuted(!snap.Muted)
	}
	if c.Shortcut(ui.Cmd, ui.KeyO) && !c.ShortcutRepeated() {
		a.pickMediaFile()
	}

	white := ui.RGB(245, 245, 247)
	mutedText := ui.RGB(185, 185, 195)
	hudBg := ui.RGBA(18, 18, 22, 220)

	ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
		// Zero-copy GPU video canvas.
		player.View(c, a.p).Fill()

		// Center state overlay (idle prompt, buffering spinner, or error banner).
		ui.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(12).Children(func() {
			switch {
			case a.lastErr != "":
				ui.Text(c, "Error: "+a.lastErr).TextColor(ui.RGB(255, 110, 110)).Background(hudBg).Padding(12).Radius(8)
			case snap.Err != nil:
				ui.Text(c, "Playback error: "+snap.Err.Error()).TextColor(ui.RGB(255, 110, 110)).Background(hudBg).Padding(12).Radius(8)
			case snap.State == player.StateIdle:
				ui.Column(c).Center().Gap(10).Padding(20).Background(hudBg).Radius(12).Children(func() {
					ui.Text(c, "MyGo Player (libmpv Zero-Copy)").FontSize(20).Bold().TextColor(white)
					ui.Text(c, "Open a local MKV/MP4 file or pass a path/URL on the command line.").TextColor(mutedText)
					if ui.PrimaryButton(c.Key("open-empty"), "Open Video (Cmd+O)").Clicked() {
						a.pickMediaFile()
					}
				})
			case snap.State == player.StateLoading || snap.Buffering:
				ui.Spinner(c)
			}
		})

		// Top info bar (status, active hwdec, resolution, rendered frames).
		ui.Row(c).Absolute().Top(12).Left(12).Right(12).Padding(8).PaddingX(12).Gap(10).AlignItems(ui.Center).Background(hudBg).Radius(10).Children(func() {
			title := snap.MediaTitle
			if title == "" && a.filePath != "" {
				title = filepath.Base(a.filePath)
			}
			if title == "" {
				title = "No media loaded"
			}
			ui.Text(c, title).Bold().TextColor(white).Grow(1)

			hwLabel := "SW"
			if snap.CurrentHWDec != "" && snap.CurrentHWDec != "no" {
				hwLabel = "HW:" + snap.CurrentHWDec
			}
			stats := fmt.Sprintf("%s  |  %dx%d  |  %d frames", hwLabel, snap.VideoWidth, snap.VideoHeight, a.p.FramesRendered())
			ui.Text(c, stats).FontSize(12).TextColor(mutedText).FontFeatures("tnum")

			if ui.Button(c.Key("open-top"), "Open...").Clicked() {
				a.pickMediaFile()
			}
		})

		// Bottom playback controls HUD.
		ui.Column(c).Absolute().Left(12).Right(12).Bottom(12).Padding(10).Gap(8).Background(hudBg).Radius(12).Children(func() {
			// Seek bar row.
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, formatClock(snap.Position)).FontSize(12).TextColor(white).FontFeatures("tnum").Width(54)
				pos := snap.Position.Seconds()
				dur := snap.Duration.Seconds()
				if ui.Slider(c.Key("seek"), &pos, 0, max(dur, 0.001)).Grow(1).Label("Seek").Changed() {
					_ = a.p.Seek(time.Duration(pos * float64(time.Second)))
				}
				ui.Text(c, formatClock(snap.Duration)).FontSize(12).TextColor(mutedText).FontFeatures("tnum").Width(54)
			})

			// Transport + M2 track / subtitle / hwdec controls row.
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				playLabel := "Pause"
				if snap.Paused || snap.State == player.StateEnded || snap.State == player.StateIdle {
					playLabel = "Play"
				}
				if ui.Button(c.Key("play-pause"), playLabel).Width(68).Clicked() {
					_ = a.p.TogglePause()
				}
				if ui.Button(c.Key("back5"), "-5s").Clicked() {
					_ = a.p.SeekRelative(-5 * time.Second)
				}
				if ui.Button(c.Key("fwd5"), "+5s").Clicked() {
					_ = a.p.SeekRelative(5 * time.Second)
				}

				// Volume + Mute.
				muteLabel := "Mute"
				if snap.Muted {
					muteLabel = "Unmute"
				}
				if ui.Button(c.Key("mute"), muteLabel).Width(70).Clicked() {
					_ = a.p.SetMuted(!snap.Muted)
				}
				vol := snap.Volume
				if ui.Slider(c.Key("volume"), &vol, 0, 100).Width(90).Label("Volume").Changed() {
					_ = a.p.SetVolume(vol)
				}

				ui.Box(c).Grow(1)

				// Speed selector.
				speedLabel := fmt.Sprintf("%.2gx", snap.Speed)
				ui.PopoverMenuButton(c.Key("speed"), speedLabel, func(m *ui.Menu) {
					for _, spd := range []float64{0.5, 0.75, 1.0, 1.25, 1.5, 2.0} {
						lbl := fmt.Sprintf("%.2gx", spd)
						if m.Item(lbl).Checked(absDiff(snap.Speed, spd) < 0.01).Chosen() {
							_ = a.p.SetSpeed(spd)
						}
					}
				})

				// M2: Audio track switcher.
				audioTracks := filterByType(snap.Tracks, player.TrackAudio)
				ui.PopoverMenuButton(c.Key("audio-tracks"), fmt.Sprintf("Audio (%d)", len(audioTracks)), func(m *ui.Menu) {
					if m.Item("Disable Audio").Checked(snap.AudioTrackID == 0).Chosen() {
						_ = a.p.DisableAudioTrack()
					}
					if len(audioTracks) > 0 {
						m.Separator()
					}
					for _, tr := range audioTracks {
						if m.Item(trackLabel(tr)).ID(fmt.Sprintf("aid-%d", tr.ID)).Checked(tr.Selected).Chosen() {
							_ = a.p.SelectAudioTrack(tr.ID)
						}
					}
				})

				// M2: Embedded & external subtitle switcher.
				subTracks := filterByType(snap.Tracks, player.TrackSub)
				ui.PopoverMenuButton(c.Key("sub-tracks"), fmt.Sprintf("Sub (%d)", len(subTracks)), func(m *ui.Menu) {
					if m.Item("Load External Subtitle...").Chosen() {
						a.pickSubtitleFile()
					}
					visLabel := "Hide Subtitles"
					if !snap.SubVisible {
						visLabel = "Show Subtitles"
					}
					if m.Item(visLabel).Chosen() {
						_ = a.p.SetSubtitleVisible(!snap.SubVisible)
					}
					if m.Item("Disable Subtitles").Checked(snap.SubTrackID == 0).Chosen() {
						_ = a.p.DisableSubtitleTrack()
					}
					if len(subTracks) > 0 {
						m.Separator()
					}
					for _, tr := range subTracks {
						if m.Item(trackLabel(tr)).ID(fmt.Sprintf("sid-%d", tr.ID)).Checked(tr.Selected).Chosen() {
							_ = a.p.SelectSubtitleTrack(tr.ID)
						}
					}
				})

				// M2: Hardware decoding mode selector.
				ui.PopoverMenuButton(c.Key("hwdec"), "HWDec", func(m *ui.Menu) {
					modes := []struct {
						label string
						mode  player.HWDecMode
					}{
						{"Auto Safe (VideoToolbox)", player.HWDecAutoSafe},
						{"VideoToolbox (Zero-Copy)", player.HWDecVideoToolbox},
						{"VideoToolbox (Copy-Back)", player.HWDecVideoToolboxCopy},
						{"Software (Disabled)", player.HWDecNone},
					}
					for _, item := range modes {
						if m.Item(item.label).Checked(a.hwdecMode == item.mode).Chosen() {
							a.hwdecMode = item.mode
							if err := a.p.SetHWDec(item.mode); err != nil {
								a.lastErr = err.Error()
							}
						}
					}
				})
			})
		})
	})
}

func filterByType(tracks []player.Track, kind player.TrackType) []player.Track {
	var out []player.Track
	for _, tr := range tracks {
		if tr.Type == kind {
			out = append(out, tr)
		}
	}
	return out
}

func trackLabel(tr player.Track) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("#%d", tr.ID))
	if tr.Title != "" {
		parts = append(parts, tr.Title)
	}
	if tr.Lang != "" {
		parts = append(parts, "["+tr.Lang+"]")
	}
	if tr.Codec != "" {
		parts = append(parts, "("+tr.Codec+")")
	}
	if tr.External {
		parts = append(parts, "External")
	}
	return strings.Join(parts, " ")
}

func formatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d.Round(time.Second) / time.Second)
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func init() {
	// Ensure stderr log output is clean in CLI invocations.
	log.SetOutput(os.Stderr)
}
