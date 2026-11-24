package player

import "time"

// HWDecMode controls hardware video decoding in libmpv.
type HWDecMode string

const (
	// HWDecAutoSafe enables safe zero-copy hardware decoding (VideoToolbox on
	// macOS) and falls back to software decoding for unsupported streams.
	HWDecAutoSafe HWDecMode = "auto-safe"
	// HWDecAutoCopy enables safe copy-back hardware decoding (such as D3D11VA on
	// Windows, VA-API/NVDEC on Linux, or VideoToolbox copy-back on macOS) and
	// falls back to software decoding for unsupported streams. Default on Windows
	// and Linux.
	HWDecAutoCopy HWDecMode = "auto-copy"
	// HWDecVideoToolbox explicitly requests Apple VideoToolbox zero-copy decoding.
	HWDecVideoToolbox HWDecMode = "videotoolbox"
	// HWDecVideoToolboxCopy requests VideoToolbox decoding with copy-back.
	HWDecVideoToolboxCopy HWDecMode = "videotoolbox-copy"
	// HWDecD3D11VACopy explicitly requests Windows D3D11VA hardware decoding with copy-back.
	HWDecD3D11VACopy HWDecMode = "d3d11va-copy"
	// HWDecVAAPICopy explicitly requests Linux VA-API hardware decoding with copy-back.
	HWDecVAAPICopy HWDecMode = "vaapi-copy"
	// HWDecNone disables hardware decoding (software decoding only).
	HWDecNone HWDecMode = "no"
)

// SubtitleFlag controls how AddSubtitleFile treats a newly added subtitle track.
type SubtitleFlag string

const (
	// SubtitleSelect loads and immediately activates the subtitle track (default).
	SubtitleSelect SubtitleFlag = "select"
	// SubtitleAuto loads the subtitle track without overriding an active track.
	SubtitleAuto SubtitleFlag = "auto"
	// SubtitleCached loads the subtitle track without selecting it.
	SubtitleCached SubtitleFlag = "cached"
)

// TrackType identifies the kind of media stream in a Track.
type TrackType string

const (
	TrackVideo TrackType = "video"
	TrackAudio TrackType = "audio"
	TrackSub   TrackType = "sub"
)

// Track describes a video, audio, or subtitle stream reported by libmpv.
type Track struct {
	ID               int64
	Type             TrackType
	SrcID            int64
	Title            string
	Lang             string
	Codec            string
	Selected         bool
	Default          bool
	Forced           bool
	External         bool
	ExternalFilename string
	Width            int
	Height           int
	FPS              float64
	Channels         int
	SampleRate       int
}

// State represents the playback lifecycle state of a Player.
type State int

const (
	StateIdle State = iota
	StateLoading
	StatePlaying
	StatePaused
	StateBuffering
	StateEnded
	StateError
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateLoading:
		return "loading"
	case StatePlaying:
		return "playing"
	case StatePaused:
		return "paused"
	case StateBuffering:
		return "buffering"
	case StateEnded:
		return "ended"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// Options configures a new Player created by New.
type Options struct {
	// HWDec configures hardware decoding. Defaults to HWDecAutoSafe on macOS
	// and HWDecAutoCopy on Windows and Linux.
	HWDec HWDecMode
	// KeepOpen keeps the last frame visible when playback reaches EOF.
	// Defaults to true when KeepOpen is nil.
	KeepOpen *bool
	// Volume sets initial volume in [0, 100] (or higher for software boost).
	// Defaults to 100 when <= 0 and StartMuted is false.
	Volume float64
	// StartMuted mutes audio on creation.
	StartMuted bool
	// Loop loops the current file indefinitely when true.
	Loop bool
	// HTTPHeaders sets default HTTP headers sent for network streams.
	HTTPHeaders map[string]string
	// UserAgent sets the default HTTP User-Agent for network streams.
	UserAgent string
	// Referrer sets the default HTTP Referer header for network streams.
	Referrer string
	// AudioOutput overrides mpv's "ao" option (e.g. "null" for headless tests).
	// Defaults to "wasapi" on Windows and mpv's native default on macOS
	// (CoreAudio) and Linux (PipeWire/PulseAudio/ALSA).
	AudioOutput string
	// LogLevel enables libmpv log callbacks ("fatal", "error", "warn", "info", "v", "debug").
	// Empty disables log message delivery.
	LogLevel string
	// ExtraOptions sets arbitrary libmpv options before mpv_initialize.
	ExtraOptions map[string]string
	// OnLog receives libmpv log lines when LogLevel is set.
	// Runs on the player's background event goroutine.
	OnLog func(level, prefix, text string)
	// OnStateChange is invoked when playback state, tracks, or metadata change.
	// Runs on the player's background event goroutine; calls to Player methods are safe.
	OnStateChange func()
	// OnEndFile is invoked when playback of a file ends (EOF, stop, or error).
	// Runs on the player's background event goroutine.
	OnEndFile func(err error)
}

// OpenOptions configures per-file playback parameters passed to Player.Open.
type OpenOptions struct {
	// Headers overrides or supplements HTTP headers for this media source.
	// Per-open headers are automatically reset on subsequent Open calls.
	Headers map[string]string
	// UserAgent overrides the HTTP User-Agent for this media source.
	UserAgent string
	// Referrer overrides the HTTP Referer for this media source.
	Referrer string
	// StartTime seeks to the given position when opening the media.
	StartTime time.Duration
	// Paused opens the media paused on its first frame.
	Paused bool
	// Subtitles specifies external subtitle files to attach automatically.
	Subtitles []string
}

// Snapshot is an immutable point-in-time summary of player state suitable for
// reading in UI view functions without taking locks repeatedly.
type Snapshot struct {
	State        State
	Position     time.Duration
	Duration     time.Duration
	Volume       float64
	Muted        bool
	Speed        float64
	Paused       bool
	Buffering    bool
	EOF          bool
	VideoWidth   int
	VideoHeight  int
	CurrentHWDec string
	MediaTitle   string
	AudioTrackID int64
	SubTrackID   int64
	SubVisible   bool
	Tracks       []Track
	Err          error
}
