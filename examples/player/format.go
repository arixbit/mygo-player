package main

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	player "github.com/arixbit/mygo-player"
)

// formatClock formats a playback time. Times of media an hour or longer
// always show hours, so the labels keep their width as the clock runs.
func formatClock(d time.Duration, withHours bool) string {
	if d < 0 {
		d = 0
	}
	sec := int(d / time.Second)
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 || withHours {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatRemaining formats the time left, as Apple's players show it at the
// end of the seek bar.
func formatRemaining(pos, dur time.Duration) string {
	left := dur - pos
	if left < 0 {
		left = 0
	}
	// Round up, so the label reaches 0:00 exactly when playback ends.
	left = (left + time.Second - 1).Truncate(time.Second)
	return "-" + formatClock(left, dur >= time.Hour)
}

// speedLabel formats a playback rate as "1×", "1.25×".
func speedLabel(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64) + "×"
}

var speeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 2}

func filterByType(tracks []player.Track, kind player.TrackType) []player.Track {
	var out []player.Track
	for _, tr := range tracks {
		if tr.Type == kind {
			out = append(out, tr)
		}
	}
	return out
}

// trackNames names tracks for a menu with Track.DisplayName, telling apart
// tracks that would share a name by their codec, then by their number.
func trackNames(tracks []player.Track) []string {
	names := make([]string, len(tracks))
	count := map[string]int{}
	for i, tr := range tracks {
		names[i] = tr.DisplayName()
		count[names[i]]++
	}
	for i, tr := range tracks {
		if count[names[i]] > 1 && tr.Codec != "" {
			names[i] += " · " + strings.ToUpper(tr.Codec)
		}
	}
	seen := map[string]int{}
	for i := range names {
		seen[names[i]]++
	}
	for i, tr := range tracks {
		if seen[names[i]] > 1 {
			names[i] += fmt.Sprintf(" · %d", tr.ID)
		}
	}
	return names
}

var subtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true, ".sup": true, ".idx": true,
}

// isSubtitleFile reports whether path names a subtitle file by extension.
func isSubtitleFile(path string) bool {
	return subtitleExts[strings.ToLower(filepath.Ext(path))]
}

// videoExtensions are the extensions of media files the player opens from a
// drop without asking: video, and audio, which libmpv plays too.
var videoExtensions = []string{
	"mkv", "mk3d", "mp4", "m4v", "mov", "webm", "avi", "ts", "m2ts", "mts", "flv", "f4v", "wmv", "asf",
	"mpg", "mpeg", "vob", "ogv", "ogm", "3gp", "3g2", "rm", "rmvb", "divx", "mxf", "y4m",
	"mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "wma", "alac", "aiff", "ape",
}

var videoExts = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range videoExtensions {
		m["."+e] = true
	}
	return m
}()

// splitDropped sorts dropped or opened paths into the first media file, the
// first file that is neither media nor subtitles, and the subtitle files.
func splitDropped(paths []string) (video, other string, subs []string) {
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		switch {
		case isSubtitleFile(p):
			subs = append(subs, p)
		case videoExts[ext] || strings.Contains(p, "://"):
			if video == "" {
				video = p
			}
		case other == "":
			other = p
		}
	}
	return video, other, subs
}

// mediaName is what to call the media: its title when it has one, else its
// file name, or the last part of its URL's path. Query strings and
// fragments of URLs, which may carry credentials, never show, also when
// libmpv made the title from the URL.
func mediaName(title, target string) string {
	if !strings.Contains(target, "://") {
		if title != "" {
			return title
		}
		if target == "" {
			return ""
		}
		return filepath.Base(target)
	}
	clean, raw := target, target[strings.LastIndex(target, "/")+1:]
	if u, err := url.Parse(target); err == nil {
		clean = u.Host
		if b := path.Base(u.Path); b != "." && b != "/" && b != "" {
			clean = b
		}
	} else if i := strings.IndexAny(raw, "?#"); i >= 0 {
		clean = raw[:i]
	}
	// libmpv names a stream without a title after its URL (percent-decoded,
	// from the last slash, which may be inside the query): any title found
	// in the URL is that, and only a title of the media's own is shown.
	if title == "" || fromURL(title, target) {
		return clean
	}
	return title
}

// fromURL reports whether title is part of url as written or decoded.
func fromURL(title, u string) bool {
	if strings.Contains(u, title) {
		return true
	}
	if d, err := url.QueryUnescape(u); err == nil && strings.Contains(d, title) {
		return true
	}
	d, err := url.PathUnescape(u)
	return err == nil && strings.Contains(d, title)
}

// isLocalFile reports whether target is a path rather than a URL.
func isLocalFile(target string) bool {
	return target != "" && !strings.Contains(target, "://")
}

var errorCode = regexp.MustCompile(`\s*\(-?\d+\)$`)

// friendlyError turns a player error into a sentence for people: without
// the package prefixes and libmpv's error number, capitalized, with a
// period ("mpv playback: unrecognized file format (-17)" reads "Unrecognized
// file format.").
func friendlyError(err error) string {
	msg := err.Error()
	for _, p := range []string{"player: ", "mpv playback: ", "mpv: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	msg = strings.TrimSpace(errorCode.ReplaceAllString(msg, ""))
	if msg == "" {
		return "Something went wrong."
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}
