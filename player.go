package player

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/egoist/mygo/native"
	"github.com/egoist/mygo/ui"

	"github.com/arixbit/mygo-player/internal/mpv"
)

// ErrClosed is returned when operations are invoked on a closed Player.
var ErrClosed = errors.New("player: closed")

type frameRenderer interface {
	SetViewportSize(w, h int)
	SetVideoSize(w, h int)
	RequestRedraw()
	FramesRendered() uint64
	FrameSize() (w, h int)
	ImageSource() ui.ImageSource
	Close()
}

// Property observation userdata tokens.
const (
	propPause uint64 = iota + 1
	propTimePos
	propDuration
	propVolume
	propMute
	propSpeed
	propEOFReached
	propPausedForCache
	propVideoDW
	propVideoDH
	propHWDecCurrent
	propAID
	propSID
	propSubVisibility
	propTrackListCount
	propMediaTitle
	propSubDelay
	propAudioDelay
	propSubScale
	propSubPos
	propVideoRotate
	propPanscan
)

// posNotifyInterval throttles OnStateChange/Invalidate notifications caused
// solely by time-pos updates. Snapshot().Position is still updated on every
// libmpv time-pos event; state changes and seeks notify immediately, and a
// throttled trailing update is always flushed so the last position is shown.
const posNotifyInterval = 100 * time.Millisecond

type observedProp struct {
	id     uint64
	name   string
	format mpv.Format
}

// observedProperties lists every libmpv property mirrored into Snapshot.
var observedProperties = []observedProp{
	{propPause, "pause", mpv.FormatFlag},
	{propTimePos, "time-pos", mpv.FormatDouble},
	{propDuration, "duration", mpv.FormatDouble},
	{propVolume, "volume", mpv.FormatDouble},
	{propMute, "mute", mpv.FormatFlag},
	{propSpeed, "speed", mpv.FormatDouble},
	{propEOFReached, "eof-reached", mpv.FormatFlag},
	{propPausedForCache, "paused-for-cache", mpv.FormatFlag},
	{propVideoDW, "video-params/dw", mpv.FormatInt64},
	{propVideoDH, "video-params/dh", mpv.FormatInt64},
	{propHWDecCurrent, "hwdec-current", mpv.FormatString},
	{propAID, "aid", mpv.FormatString},
	{propSID, "sid", mpv.FormatString},
	{propSubVisibility, "sub-visibility", mpv.FormatFlag},
	{propTrackListCount, "track-list/count", mpv.FormatInt64},
	{propMediaTitle, "media-title", mpv.FormatString},
	{propSubDelay, "sub-delay", mpv.FormatDouble},
	{propAudioDelay, "audio-delay", mpv.FormatDouble},
	{propSubScale, "sub-scale", mpv.FormatDouble},
	{propSubPos, "sub-pos", mpv.FormatDouble},
	{propVideoRotate, "video-rotate", mpv.FormatString},
	{propPanscan, "panscan", mpv.FormatDouble},
}

// Player manages a libmpv playback instance rendered into MyGo's native UI
// scene graph (zero-copy native.Texture on macOS; double-buffered ui.Bitmap on
// Windows). Its methods are safe to call from any goroutine.
type Player struct {
	client   *mpv.Client
	tex      *native.Texture
	renderer frameRenderer

	opts            Options
	defaultHeaders  map[string]string
	defaultUA       string
	defaultReferrer string

	keepOpen bool

	cmdMu      sync.Mutex // serializes open/close/option mutation with mpv
	closing    atomic.Bool
	closed     atomic.Bool
	lastTarget string // guarded by cmdMu; reloaded by Play after keep-open=no EOF

	eventDone chan struct{}
	// releaseReady is closed by Close once the renderer is released, so the
	// mpv handle can be destroyed (render context must be freed first).
	releaseReady chan struct{}
	destroyOnce  sync.Once
	eventGID     atomic.Uint64 // goroutine ID of eventLoop

	// time-pos notification throttling (see posNotifyInterval).
	lastNotify atomic.Int64 // UnixNano of the last notifyUI
	posForce   atomic.Bool  // next time-pos notifies immediately (seek/open)
	posPending atomic.Bool  // a throttled time-pos update awaits flushing

	mu          sync.RWMutex
	snap        Snapshot
	fileLoaded  bool
	eofNotified bool // OnEndFile already delivered for the current file's EOF
	invalidate  func()
}

// New creates and initializes a libmpv Player integrated with MyGo's UI.
func New(opts ...Options) (*Player, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	client, err := mpv.Create()
	if err != nil {
		return nil, err
	}

	ok := false
	defer func() {
		if !ok {
			client.TerminateDestroy()
		}
	}()

	// Required base configuration for embedded libmpv rendering in MyGo.
	keepOpen := true
	if opt.KeepOpen != nil {
		keepOpen = *opt.KeepOpen
	}
	hwdec := opt.HWDec
	if hwdec == "" {
		hwdec = defaultHWDec()
	}
	// Volume <= 0 means "default" (100) even with StartMuted, so unmuting a
	// muted-at-start player is audible; muting is applied via mute=yes.
	vol := opt.Volume
	if vol <= 0 {
		vol = 100
	}

	baseOpts := [][2]string{
		{"vo", "libmpv"},
		{"vid", "auto"},
		{"idle", "yes"},
		{"config", "no"},
		{"osd-level", "0"},
		{"input-default-bindings", "no"},
		{"input-vo-keyboard", "no"},
		{"sub-auto", "fuzzy"},
		{"hwdec", string(hwdec)},
		{"volume", strconv.FormatFloat(vol, 'f', 2, 64)},
	}
	if keepOpen {
		baseOpts = append(baseOpts, [2]string{"keep-open", "yes"})
	} else {
		baseOpts = append(baseOpts, [2]string{"keep-open", "no"})
	}
	if opt.StartMuted {
		baseOpts = append(baseOpts, [2]string{"mute", "yes"})
	}
	if opt.Loop {
		baseOpts = append(baseOpts, [2]string{"loop-file", "inf"})
	}
	ao := opt.AudioOutput
	if ao == "" {
		ao = defaultAudioOutput()
	}
	if ao != "" {
		baseOpts = append(baseOpts, [2]string{"ao", ao})
	}
	for _, kv := range baseOpts {
		if err := client.SetOptionString(kv[0], kv[1]); err != nil {
			return nil, fmt.Errorf("player: set option %s=%s: %w", kv[0], kv[1], err)
		}
	}

	// Best-effort disable built-in Lua script options when libmpv is built with
	// Lua enabled; minimal Lua-less libmpv builds return ErrOptionNotFound.
	for _, kv := range [][2]string{{"osc", "no"}, {"ytdl", "no"}} {
		if err := client.SetOptionString(kv[0], kv[1]); err != nil {
			var mpvErr *mpv.Error
			if !errors.As(err, &mpvErr) || mpvErr.Code != mpv.ErrOptionNotFound {
				return nil, fmt.Errorf("player: set option %s=%s: %w", kv[0], kv[1], err)
			}
		}
	}

	// Apply extra options sorted for deterministic ordering.
	if len(opt.ExtraOptions) > 0 {
		keys := slices.Sorted(maps.Keys(opt.ExtraOptions))
		for _, k := range keys {
			if err := client.SetOptionString(k, opt.ExtraOptions[k]); err != nil {
				return nil, fmt.Errorf("player: set extra option %s: %w", k, err)
			}
		}
	}

	if opt.LogLevel != "" {
		if err := client.RequestLogMessages(opt.LogLevel); err != nil {
			return nil, fmt.Errorf("player: request log messages: %w", err)
		}
	}

	if err := client.Initialize(); err != nil {
		return nil, fmt.Errorf("player: initialize libmpv: %w", err)
	}

	p := &Player{
		client:          client,
		opts:            opt,
		keepOpen:        keepOpen,
		releaseReady:    make(chan struct{}),
		defaultHeaders:  maps.Clone(opt.HTTPHeaders),
		defaultUA:       opt.UserAgent,
		defaultReferrer: opt.Referrer,
		eventDone:       make(chan struct{}),
		snap: Snapshot{
			State:      StateIdle,
			Volume:     vol,
			Muted:      opt.StartMuted,
			Speed:      1.0,
			SubVisible: true,
			SubScale:   1.0,
			SubPos:     100,
		},
	}

	tex, renderer, err := newRenderer(client, p.notifyFrame)
	if err != nil {
		return nil, err
	}
	p.tex = tex
	p.renderer = renderer

	if err := p.applyHTTPConfig(p.defaultHeaders, p.defaultUA, p.defaultReferrer); err != nil {
		renderer.Close()
		return nil, err
	}

	if err := p.observeProperties(); err != nil {
		renderer.Close()
		return nil, err
	}

	ok = true
	go p.eventLoop()
	return p, nil
}

func (p *Player) observeProperties() error {
	for _, o := range observedProperties {
		if err := p.client.ObserveProperty(o.id, o.name, o.format); err != nil {
			return fmt.Errorf("player: observe %s: %w", o.name, err)
		}
	}
	return nil
}

// Texture returns the zero-copy GPU native.Texture on macOS, or nil on
// platforms that present frames through ui.Bitmap (such as Windows). Prefer
// View(c, p) or ImageSource() for cross-platform UI rendering.
func (p *Player) Texture() *native.Texture {
	if p == nil {
		return nil
	}
	return p.tex
}

// ImageSource returns the ui.ImageSource for the current video frame
// (*native.Texture on macOS, *ui.Bitmap on Windows). Call it on the UI thread
// (typically via View(c, p)).
func (p *Player) ImageSource() ui.ImageSource {
	if p == nil || p.renderer == nil {
		return nil
	}
	return p.renderer.ImageSource()
}

// FrameSize returns the pixel dimensions of the most recently rendered video
// frame on any supported platform.
func (p *Player) FrameSize() (width, height int) {
	if p == nil || p.renderer == nil {
		return 0, 0
	}
	return p.renderer.FrameSize()
}

// SetRenderSize sets the target physical pixel dimensions for video rendering.
// Pass (0, 0) to revert to the video's native resolution.
func (p *Player) SetRenderSize(width, height int) {
	if p == nil || p.closed.Load() || p.renderer == nil {
		return
	}
	p.renderer.SetViewportSize(width, height)
}

// FramesRendered returns the total number of video frames rendered since creation.
func (p *Player) FramesRendered() uint64 {
	if p == nil || p.renderer == nil {
		return 0
	}
	return p.renderer.FramesRendered()
}

// Snapshot returns a consistent copy of the player's current playback state and tracks.
func (p *Player) Snapshot() Snapshot {
	p.mu.RLock()
	s := p.snap
	if len(s.Tracks) > 0 {
		s.Tracks = slices.Clone(s.Tracks)
	}
	p.mu.RUnlock()
	return s
}

// Open opens a local file path or network URL for playback.
//
// Open resets every per-file Snapshot field left over from the previous media
// (position, duration, EOF, buffering, title, video size, track IDs, tracks,
// error) and synchronizes Snapshot().Paused with opt.Paused immediately.
func (p *Player) Open(target string, opts ...OpenOptions) error {
	if target == "" {
		return errors.New("player: empty media target")
	}
	var opt OpenOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	p.cmdMu.Lock()
	if p.closed.Load() {
		p.cmdMu.Unlock()
		return ErrClosed
	}
	changed, err := p.openLocked(target, opt)
	p.cmdMu.Unlock()
	// Notify outside cmdMu so OnStateChange may call Player methods (or Close).
	if changed {
		p.notifyUI()
	}
	return err
}

func (p *Player) openLocked(target string, opt OpenOptions) (changed bool, err error) {
	// Merge default and per-open HTTP configuration, always resetting previous
	// per-open headers so credentials never leak across opens. Per-open keys
	// override defaults case-insensitively ("authorization" replaces
	// "Authorization").
	headers := mergeHeaders(p.defaultHeaders, opt.Headers)
	ua := p.defaultUA
	if opt.UserAgent != "" {
		ua = opt.UserAgent
	}
	ref := p.defaultReferrer
	if opt.Referrer != "" {
		ref = opt.Referrer
	}
	if err := p.applyHTTPConfig(headers, ua, ref); err != nil {
		return false, err
	}

	if err := p.client.SetPropertyFlag("pause", opt.Paused); err != nil {
		return false, err
	}
	if opt.StartTime > 0 {
		if err := p.client.SetPropertyString("start", strconv.FormatFloat(opt.StartTime.Seconds(), 'f', 3, 64)); err != nil {
			return false, err
		}
	} else {
		_ = p.client.SetPropertyString("start", "none")
	}
	if len(opt.Subtitles) > 0 {
		if err := p.client.SetPropertyStringList("sub-files", opt.Subtitles); err != nil {
			return false, err
		}
	} else {
		_ = p.client.SetPropertyStringList("sub-files", nil)
	}

	p.mu.Lock()
	p.fileLoaded = false
	p.resetMediaLocked()
	p.snap.State = StateLoading
	p.snap.Paused = opt.Paused
	p.mu.Unlock()
	p.posForce.Store(true)
	p.lastTarget = target

	if err := p.client.Command("loadfile", target, "replace"); err != nil {
		p.mu.Lock()
		p.snap.State = StateError
		p.snap.Err = err
		p.mu.Unlock()
		return true, err
	}
	return true, nil
}

// resetMediaLocked clears every per-file Snapshot field. Caller holds p.mu.
func (p *Player) resetMediaLocked() {
	p.snap.EOF = false
	p.snap.Position = 0
	p.snap.Duration = 0
	p.snap.Buffering = false
	p.snap.MediaTitle = ""
	p.snap.VideoWidth = 0
	p.snap.VideoHeight = 0
	p.snap.AudioTrackID = 0
	p.snap.SubTrackID = 0
	p.snap.Tracks = nil
	p.snap.Err = nil
}

// Play starts or resumes playback. If playback reached EOF it restarts from
// the beginning: with KeepOpen (default) it seeks back to 0; with
// KeepOpen=false (file already unloaded) it reloads the last opened target.
func (p *Player) Play() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.mu.RLock()
	eof := p.snap.EOF
	loaded := p.fileLoaded
	p.mu.RUnlock()
	if eof {
		p.posForce.Store(true)
		if !loaded && p.lastTarget != "" {
			_ = p.client.SetPropertyString("start", "none")
			if err := p.client.SetPropertyFlag("pause", false); err != nil {
				return err
			}
			return p.client.Command("loadfile", p.lastTarget, "replace")
		}
		if err := p.client.Command("seek", "0", "absolute"); err != nil {
			return err
		}
	}
	return p.client.SetPropertyFlag("pause", false)
}

// Pause pauses playback.
func (p *Player) Pause() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	return p.client.SetPropertyFlag("pause", true)
}

// TogglePause switches between playing and paused states. When playback is
// paused or has reached EOF it calls Play (which restarts ended media).
func (p *Player) TogglePause() error {
	s := p.Snapshot()
	if s.Paused || s.EOF {
		return p.Play()
	}
	return p.Pause()
}

// Stop stops playback and returns the player to idle, clearing every per-file
// Snapshot field (EOF, position, duration, buffering, title, video size,
// track IDs, tracks and error).
func (p *Player) Stop() error {
	p.cmdMu.Lock()
	if p.closed.Load() {
		p.cmdMu.Unlock()
		return ErrClosed
	}
	err := p.client.Command("stop")
	if err == nil {
		p.mu.Lock()
		p.fileLoaded = false
		p.resetMediaLocked()
		p.snap.State = StateIdle
		p.mu.Unlock()
	}
	p.cmdMu.Unlock()
	if err == nil {
		p.notifyUI()
	}
	return err
}

// Seek seeks to an absolute playback timestamp.
func (p *Player) Seek(pos time.Duration) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if pos < 0 {
		pos = 0
	}
	p.posForce.Store(true)
	return p.client.Command("seek", strconv.FormatFloat(pos.Seconds(), 'f', 3, 64), "absolute+exact")
}

// SeekRelative seeks forward (positive) or backward (negative) by delta.
func (p *Player) SeekRelative(delta time.Duration) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.posForce.Store(true)
	return p.client.Command("seek", strconv.FormatFloat(delta.Seconds(), 'f', 3, 64), "relative")
}

// SetVolume sets playback volume (typically 0..100).
func (p *Player) SetVolume(v float64) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if v < 0 {
		v = 0
	}
	return p.client.SetPropertyFloat64("volume", v)
}

// SetMuted mutes or unmutes audio output.
func (p *Player) SetMuted(muted bool) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	return p.client.SetPropertyFlag("mute", muted)
}

// ToggleMute mutes or unmutes audio output based on libmpv's current state
// (not the asynchronously updated Snapshot), so calls made in quick
// succession each flip it. When unmuting a player whose volume is 0, it also
// restores an audible volume (50), so that unmuting is never silent.
func (p *Player) ToggleMute() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	muted, err := p.client.GetPropertyFlag("mute")
	if err != nil {
		return err
	}
	if muted {
		if vol, err := p.client.GetPropertyFloat64("volume"); err == nil && vol <= 0 {
			if err := p.client.SetPropertyFloat64("volume", 50); err != nil {
				return err
			}
		}
	}
	return p.client.SetPropertyFlag("mute", !muted)
}

// SetSpeed sets the playback rate multiplier (e.g. 0.5, 1.0, 1.5, 2.0).
func (p *Player) SetSpeed(rate float64) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if rate <= 0 {
		return errors.New("player: speed must be > 0")
	}
	return p.client.SetPropertyFloat64("speed", rate)
}

// SetLoop enables or disables infinite looping of the current file.
func (p *Player) SetLoop(loop bool) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if loop {
		return p.client.SetPropertyString("loop-file", "inf")
	}
	return p.client.SetPropertyString("loop-file", "no")
}

// --- M2: Tracks (Audio & Subtitles), External Subtitles, HTTP Headers, Hardware Decoding ---

// Tracks returns all tracks reported by libmpv for the current file.
func (p *Player) Tracks() []Track {
	return p.Snapshot().Tracks
}

// AudioTracks returns all audio tracks in the current media.
func (p *Player) AudioTracks() []Track {
	return filterTracks(p.Snapshot().Tracks, TrackAudio)
}

// SubtitleTracks returns all embedded and external subtitle tracks in the current media.
func (p *Player) SubtitleTracks() []Track {
	return filterTracks(p.Snapshot().Tracks, TrackSub)
}

// VideoTracks returns all video tracks in the current media.
func (p *Player) VideoTracks() []Track {
	return filterTracks(p.Snapshot().Tracks, TrackVideo)
}

// SelectAudioTrack switches to the audio track with the given mpv track ID.
func (p *Player) SelectAudioTrack(id int64) error {
	return p.do(func() error {
		if err := p.client.SetPropertyInt64("aid", id); err != nil {
			return err
		}
		p.refreshTracks()
		return nil
	}, true)
}

// DisableAudioTrack disables audio playback ("aid=no").
func (p *Player) DisableAudioTrack() error {
	return p.do(func() error {
		if err := p.client.SetPropertyString("aid", "no"); err != nil {
			return err
		}
		p.refreshTracks()
		return nil
	}, true)
}

// SelectSubtitleTrack switches to the embedded or external subtitle track with
// the given mpv track ID and ensures subtitle visibility is enabled.
func (p *Player) SelectSubtitleTrack(id int64) error {
	return p.do(func() error {
		if err := p.client.SetPropertyInt64("sid", id); err != nil {
			return err
		}
		_ = p.client.SetPropertyFlag("sub-visibility", true)
		p.refreshTracks()
		p.renderer.RequestRedraw()
		return nil
	}, true)
}

// DisableSubtitleTrack turns off subtitle track selection ("sid=no").
func (p *Player) DisableSubtitleTrack() error {
	return p.do(func() error {
		if err := p.client.SetPropertyString("sid", "no"); err != nil {
			return err
		}
		p.refreshTracks()
		p.renderer.RequestRedraw()
		return nil
	}, true)
}

// SetSubtitleVisible toggles subtitle rendering without changing the selected track.
func (p *Player) SetSubtitleVisible(visible bool) error {
	return p.do(func() error {
		if err := p.client.SetPropertyFlag("sub-visibility", visible); err != nil {
			return err
		}
		p.renderer.RequestRedraw()
		return nil
	}, false)
}

// AddSubtitleFile attaches an external subtitle file (SRT, ASS/SSA, WebVTT, SUP, etc.).
// By default flag is SubtitleSelect so the added subtitle is immediately active.
func (p *Player) AddSubtitleFile(path string, flags ...SubtitleFlag) error {
	if path == "" {
		return errors.New("player: empty subtitle path")
	}
	flag := SubtitleSelect
	if len(flags) > 0 && flags[0] != "" {
		flag = flags[0]
	}
	return p.do(func() error {
		if err := p.client.Command("sub-add", path, string(flag)); err != nil {
			return err
		}
		if flag == SubtitleSelect {
			_ = p.client.SetPropertyFlag("sub-visibility", true)
		}
		p.refreshTracks()
		p.renderer.RequestRedraw()
		return nil
	}, true)
}

// do runs fn under cmdMu (returning ErrClosed once closed) and, when notify
// is set and fn succeeded, notifies observers after releasing cmdMu so
// OnStateChange callbacks may safely call back into the Player.
func (p *Player) do(fn func() error, notify bool) error {
	p.cmdMu.Lock()
	if p.closed.Load() {
		p.cmdMu.Unlock()
		return ErrClosed
	}
	err := fn()
	p.cmdMu.Unlock()
	if err == nil && notify {
		p.notifyUI()
	}
	return err
}

// SetHTTPHeaders sets default HTTP headers applied to subsequent network streams.
// Special headers "User-Agent" and "Referer"/"Referrer" are automatically mapped
// to libmpv's dedicated user-agent and referrer properties.
func (p *Player) SetHTTPHeaders(headers map[string]string) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.defaultHeaders = maps.Clone(headers)
	return p.applyHTTPConfig(p.defaultHeaders, p.defaultUA, p.defaultReferrer)
}

// SetUserAgent sets the default HTTP User-Agent applied to network streams.
func (p *Player) SetUserAgent(ua string) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.defaultUA = ua
	return p.applyHTTPConfig(p.defaultHeaders, p.defaultUA, p.defaultReferrer)
}

// SetReferrer sets the default HTTP Referer header applied to network streams.
func (p *Player) SetReferrer(ref string) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.defaultReferrer = ref
	return p.applyHTTPConfig(p.defaultHeaders, p.defaultUA, p.defaultReferrer)
}

// SetHWDec dynamically updates the hardware decoding mode (e.g. HWDecAutoSafe,
// HWDecAutoCopy, HWDecVideoToolbox, HWDecD3D11VACopy, HWDecNone).
func (p *Player) SetHWDec(mode HWDecMode) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if mode == "" {
		mode = defaultHWDec()
	}
	return p.client.SetPropertyString("hwdec", string(mode))
}

// CurrentHWDec returns the active hardware decoder reported by libmpv
// (for example "videotoolbox", or "" / "no" when software decoding is active).
func (p *Player) CurrentHWDec() string {
	return p.Snapshot().CurrentHWDec
}

// SetPropertyString is an escape hatch for setting an arbitrary libmpv property.
func (p *Player) SetPropertyString(name, value string) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	return p.client.SetPropertyString(name, value)
}

// GetPropertyString is an escape hatch for querying an arbitrary libmpv property.
func (p *Player) GetPropertyString(name string) (string, error) {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return "", ErrClosed
	}
	return p.client.GetPropertyString(name)
}

// --- Audio/video & subtitle fine-tuning ---

// tune applies a validated property change under cmdMu, mirrors it into the
// Snapshot immediately (libmpv's property observation keeps it in sync
// afterwards), optionally requests a redraw of the current frame, and
// notifies observers outside cmdMu.
func (p *Player) tune(apply func() error, update func(*Snapshot), redraw bool) error {
	return p.do(func() error {
		if err := apply(); err != nil {
			return err
		}
		if update != nil {
			p.mu.Lock()
			update(&p.snap)
			p.mu.Unlock()
		}
		if redraw && p.renderer != nil {
			p.renderer.RequestRedraw()
		}
		return nil
	}, true)
}

// SetSubtitleDelay shifts subtitle timing by d (positive shows subtitles
// later). Mirrored in Snapshot().SubDelay.
func (p *Player) SetSubtitleDelay(d time.Duration) error {
	return p.tune(func() error {
		return p.client.SetPropertyFloat64("sub-delay", d.Seconds())
	}, func(s *Snapshot) { s.SubDelay = d }, true)
}

// SetAudioDelay shifts audio relative to video by d (positive delays audio).
// Mirrored in Snapshot().AudioDelay.
func (p *Player) SetAudioDelay(d time.Duration) error {
	return p.tune(func() error {
		return p.client.SetPropertyFloat64("audio-delay", d.Seconds())
	}, func(s *Snapshot) { s.AudioDelay = d }, false)
}

// SetSubtitleScale sets the subtitle font scale factor (must be > 0 and
// <= 100; default 1.0). Mirrored in Snapshot().SubScale.
func (p *Player) SetSubtitleScale(scale float64) error {
	return p.tune(func() error {
		if math.IsNaN(scale) || scale <= 0 || scale > 100 {
			return fmt.Errorf("player: subtitle scale %v out of range (0, 100]", scale)
		}
		return p.client.SetPropertyFloat64("sub-scale", scale)
	}, func(s *Snapshot) { s.SubScale = scale }, true)
}

// SetSubtitlePos sets the vertical subtitle position in percent of the screen
// height (0 = top, 100 = bottom (default), up to 150 to push below the
// picture). Mirrored in Snapshot().SubPos.
func (p *Player) SetSubtitlePos(pos int) error {
	return p.tune(func() error {
		if pos < 0 || pos > 150 {
			return fmt.Errorf("player: subtitle position %d out of range [0, 150]", pos)
		}
		return p.client.SetPropertyString("sub-pos", strconv.Itoa(pos))
	}, func(s *Snapshot) { s.SubPos = pos }, true)
}

// SetVideoRotate rotates the video clockwise by deg, which must be 0, 90, 180
// or 270. Mirrored in Snapshot().VideoRotate. Arbitrary angles are not
// supported by this API; use SetPropertyString("video-rotate", ...) instead.
func (p *Player) SetVideoRotate(deg int) error {
	return p.tune(func() error {
		switch deg {
		case 0, 90, 180, 270:
		default:
			return fmt.Errorf("player: video rotation %d not one of 0, 90, 180, 270", deg)
		}
		return p.client.SetPropertyString("video-rotate", strconv.Itoa(deg))
	}, func(s *Snapshot) { s.VideoRotate = deg }, true)
}

// SetPanscan crops the video to fill the View's aspect ratio: 0 (default)
// letterboxes the whole picture, 1 fills the viewport completely (cropping
// edges). Values must be within [0, 1]. Because View renders at the full
// viewport size, subtitles stay sharp and inside the visible area.
// Mirrored in Snapshot().Panscan.
func (p *Player) SetPanscan(v float64) error {
	return p.tune(func() error {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("player: panscan %v out of range [0, 1]", v)
		}
		return p.client.SetPropertyFloat64("panscan", v)
	}, func(s *Snapshot) { s.Panscan = v }, true)
}

// FrameStep pauses playback and advances exactly one video frame.
func (p *Player) FrameStep() error {
	return p.do(func() error {
		p.posForce.Store(true)
		return p.client.Command("frame-step")
	}, false)
}

// FrameBackStep pauses playback and steps back exactly one video frame
// (requires a seekable source; it is slower than FrameStep).
func (p *Player) FrameBackStep() error {
	return p.do(func() error {
		p.posForce.Store(true)
		return p.client.Command("frame-back-step")
	}, false)
}

// Close shuts down the render context, event loop, and libmpv instance cleanly.
//
// Close is idempotent and safe to call from any goroutine, including from
// inside OnStateChange / OnEndFile / OnLog callbacks running on the player's
// event goroutine. In that case Close returns immediately after stopping the
// renderer; the libmpv handle is destroyed as soon as the callback returns and
// the event loop unwinds, so no libmpv call ever touches a freed handle.
func (p *Player) Close() error {
	if p == nil {
		return nil
	}
	onEvent := p.onEventGoroutine()
	if !p.closing.CompareAndSwap(false, true) {
		if !onEvent {
			<-p.releaseReady
			<-p.eventDone
			p.destroy()
		}
		return nil
	}

	p.cmdMu.Lock()
	p.closed.Store(true)
	// Per libmpv contract: free render context BEFORE terminating mpv_handle.
	if p.renderer != nil {
		p.renderer.Close()
	}
	p.client.Wakeup()
	p.cmdMu.Unlock()
	close(p.releaseReady)

	if onEvent {
		// The event loop is blocked in our caller; it exits and destroys the
		// mpv handle once the callback returns.
		return nil
	}
	<-p.eventDone
	p.destroy()
	return nil
}

// destroy terminates the mpv handle exactly once. Callers must ensure the
// renderer is closed and the event loop no longer uses the handle.
func (p *Player) destroy() {
	p.destroyOnce.Do(func() {
		p.cmdMu.Lock()
		p.client.TerminateDestroy()
		p.cmdMu.Unlock()
	})
}

func (p *Player) onEventGoroutine() bool {
	gid := p.eventGID.Load()
	return gid != 0 && gid == curGoroutineID()
}

// curGoroutineID parses the current goroutine ID from runtime.Stack. It is
// only used on the Close path to detect re-entrant calls from callbacks.
func curGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	var id uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

func (p *Player) setInvalidate(fn func()) {
	p.mu.Lock()
	p.invalidate = fn
	p.mu.Unlock()
}

func (p *Player) notifyFrame() {
	if p.closing.Load() {
		return
	}
	p.mu.RLock()
	inv := p.invalidate
	p.mu.RUnlock()
	if inv != nil {
		inv()
	}
}

func (p *Player) notifyUI() {
	p.lastNotify.Store(time.Now().UnixNano())
	p.posPending.Store(false)
	if p.closing.Load() {
		return
	}
	p.mu.RLock()
	inv := p.invalidate
	onChange := p.opts.OnStateChange
	p.mu.RUnlock()
	if inv != nil {
		inv()
	}
	if onChange != nil {
		onChange()
	}
}

func (p *Player) applyHTTPConfig(headers map[string]string, ua, ref string) error {
	fields, effUA, effRef := splitHTTPHeaders(headers, ua, ref)
	if err := p.client.SetPropertyStringList("http-header-fields", fields); err != nil {
		return fmt.Errorf("player: set http-header-fields: %w", err)
	}
	if effUA != "" {
		if err := p.client.SetPropertyString("user-agent", effUA); err != nil {
			return fmt.Errorf("player: set user-agent: %w", err)
		}
	} else {
		// Restore mpv default UA rather than leaking a previous custom UA.
		_ = p.client.SetPropertyString("user-agent", "libmpv")
	}
	if err := p.client.SetPropertyString("referrer", effRef); err != nil {
		return fmt.Errorf("player: set referrer: %w", err)
	}
	return nil
}

// cleanHeaderKey strips CR, LF and ':' (which would allow header injection or
// malformed "Key: Value" fields) and surrounding whitespace from a header name.
func cleanHeaderKey(k string) string {
	k = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', ':':
			return -1
		}
		return r
	}, k)
	return strings.TrimSpace(k)
}

// mergeHeaders returns base overlaid with override, where override keys
// replace base keys case-insensitively (after key cleaning).
func mergeHeaders(base, override map[string]string) map[string]string {
	if len(override) == 0 {
		return maps.Clone(base)
	}
	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	for k, v := range override {
		ck := cleanHeaderKey(k)
		for ek := range out {
			if strings.EqualFold(cleanHeaderKey(ek), ck) {
				delete(out, ek)
			}
		}
		out[k] = v
	}
	return out
}

// splitHTTPHeaders separates standard headers into libmpv's http-header-fields
// list ("Key: Value") and maps User-Agent / Referer into libmpv's dedicated
// properties. Explicit ua / ref parameters take precedence over headers map.
// Keys are cleaned of CR/LF/':' and skipped when empty; values lose CR/LF.
func splitHTTPHeaders(headers map[string]string, ua, ref string) (fields []string, outUA, outRef string) {
	outUA = ua
	outRef = ref
	if len(headers) == 0 {
		return nil, outUA, outRef
	}
	keys := slices.Sorted(maps.Keys(headers))
	for _, k := range keys {
		cleanKey := cleanHeaderKey(k)
		if cleanKey == "" {
			continue
		}
		v := strings.ReplaceAll(strings.ReplaceAll(headers[k], "\r", ""), "\n", "")
		switch strings.ToLower(cleanKey) {
		case "user-agent":
			if outUA == "" {
				outUA = v
			}
		case "referer", "referrer":
			if outRef == "" {
				outRef = v
			}
		default:
			fields = append(fields, cleanKey+": "+v)
		}
	}
	return fields, outUA, outRef
}

func (p *Player) eventLoop() {
	p.eventGID.Store(curGoroutineID())
	defer func() {
		if p.closing.Load() {
			<-p.releaseReady
			p.destroy()
		}
		close(p.eventDone)
	}()
	for {
		if p.closing.Load() {
			return
		}
		timeout := -1.0
		if p.posPending.Load() {
			remain := posNotifyInterval - time.Duration(time.Now().UnixNano()-p.lastNotify.Load())
			if remain <= 0 {
				// Flush the throttled trailing time-pos update.
				p.notifyUI()
				continue
			}
			timeout = remain.Seconds()
		}
		ev := p.client.WaitEvent(timeout)
		if p.closing.Load() {
			return
		}
		if ev == nil {
			continue
		}
		switch ev.ID {
		case mpv.EventNone:
			continue
		case mpv.EventShutdown:
			return
		case mpv.EventLogMessage:
			if p.opts.OnLog != nil && ev.Data != nil {
				lm := (*mpv.LogMessageEvent)(ev.Data)
				p.opts.OnLog(
					mpv.GoString(lm.Level),
					mpv.GoString(lm.Prefix),
					strings.TrimRight(mpv.GoString(lm.Text), "\r\n"),
				)
			}
		case mpv.EventStartFile:
			p.mu.Lock()
			p.fileLoaded = false
			p.eofNotified = false
			p.snap.State = StateLoading
			p.snap.Err = nil
			p.snap.EOF = false
			p.mu.Unlock()
			p.posForce.Store(true)
			p.notifyUI()
		case mpv.EventFileLoaded:
			p.mu.Lock()
			p.fileLoaded = true
			p.recomputeStateLocked()
			p.mu.Unlock()
			p.refreshTracks()
			p.notifyUI()
		case mpv.EventSeek, mpv.EventPlaybackRestart:
			// Deliver the post-seek position without throttling.
			p.posForce.Store(true)
		case mpv.EventEndFile:
			p.handleEndFile(ev)
		case mpv.EventVideoReconfig:
			if dw, err1 := p.client.GetPropertyInt64("video-params/dw"); err1 == nil && dw > 0 {
				if dh, err2 := p.client.GetPropertyInt64("video-params/dh"); err2 == nil && dh > 0 {
					p.mu.Lock()
					p.snap.VideoWidth = int(dw)
					p.snap.VideoHeight = int(dh)
					p.mu.Unlock()
					p.renderer.SetVideoSize(int(dw), int(dh))
					p.notifyUI()
				}
			}
		case mpv.EventPropertyChange:
			if ev.Data != nil {
				p.handlePropertyChange(ev.ReplyUserdata, (*mpv.EventProperty)(ev.Data))
			}
		}
	}
}

func (p *Player) handleEndFile(ev *mpv.Event) {
	var endErr error
	reason := mpv.EndFileEOF
	if ev.Data != nil {
		ef := (*mpv.EndFileEvent)(ev.Data)
		reason = ef.Reason
		if ef.Reason == mpv.EndFileError {
			endErr = &mpv.Error{Code: ef.Error, Op: "playback", Msg: mpv.ErrorString(ef.Error)}
		}
	}
	p.mu.Lock()
	// The KeepOpen EOF callback was already delivered for this file: do not
	// report the same ending again when it is later stopped or replaced.
	deliver := endErr != nil || !p.eofNotified
	p.fileLoaded = false
	switch reason {
	case mpv.EndFileEOF:
		p.snap.EOF = true
		p.snap.State = StateEnded
		p.eofNotified = true
	case mpv.EndFileError:
		p.snap.Err = endErr
		p.snap.State = StateError
	case mpv.EndFileStop:
		p.resetMediaLocked()
		p.snap.State = StateIdle
	}
	p.mu.Unlock()
	p.notifyUI()
	if deliver && p.opts.OnEndFile != nil && !p.closing.Load() {
		p.opts.OnEndFile(endErr)
	}
}

func durationFromSeconds(sec float64) time.Duration {
	if math.IsNaN(sec) || math.IsInf(sec, 0) {
		return 0
	}
	return time.Duration(math.Round(sec*1e6)) * time.Microsecond
}

func (p *Player) handlePropertyChange(id uint64, ep *mpv.EventProperty) {
	changed := false
	tracksChanged := false
	eofReached := false
	var newW, newH int
	isNone := ep.Format == mpv.FormatNone
	hasDouble := ep.Format == mpv.FormatDouble && ep.Data != nil
	hasFlag := ep.Format == mpv.FormatFlag && ep.Data != nil
	hasInt := ep.Format == mpv.FormatInt64 && ep.Data != nil
	hasStr := ep.Format == mpv.FormatString && ep.Data != nil

	p.mu.Lock()
	switch id {
	case propPause:
		if hasFlag {
			p.snap.Paused = *(*int32)(ep.Data) != 0
			p.recomputeStateLocked()
			changed = true
		}
	case propTimePos:
		switch {
		case hasDouble && p.fileLoaded:
			p.snap.Position = max(durationFromSeconds(*(*float64)(ep.Data)), 0)
			since := time.Duration(time.Now().UnixNano() - p.lastNotify.Load())
			if p.posForce.Swap(false) || since >= posNotifyInterval {
				changed = true
			} else {
				p.posPending.Store(true)
			}
		case isNone:
			p.snap.Position = 0
			changed = true
		}
	case propDuration:
		if hasDouble {
			p.snap.Duration = max(durationFromSeconds(*(*float64)(ep.Data)), 0)
			changed = true
		} else if isNone {
			p.snap.Duration = 0
			changed = true
		}
	case propVolume:
		if hasDouble {
			p.snap.Volume = *(*float64)(ep.Data)
			changed = true
		}
	case propMute:
		if hasFlag {
			p.snap.Muted = *(*int32)(ep.Data) != 0
			changed = true
		}
	case propSpeed:
		if hasDouble {
			p.snap.Speed = *(*float64)(ep.Data)
			changed = true
		}
	case propEOFReached:
		if hasFlag {
			eof := *(*int32)(ep.Data) != 0
			p.snap.EOF = eof
			if eof {
				// KeepOpen keeps the file loaded at EOF, so no END_FILE event
				// fires: report the ending once via OnEndFile(nil).
				if p.keepOpen && p.fileLoaded && !p.eofNotified {
					p.eofNotified = true
					eofReached = true
				}
			} else if p.fileLoaded {
				// Replaying (seek back from EOF) re-arms the EOF callback.
				p.eofNotified = false
			}
			p.recomputeStateLocked()
			changed = true
		}
	case propPausedForCache:
		if hasFlag {
			p.snap.Buffering = *(*int32)(ep.Data) != 0
			p.recomputeStateLocked()
			changed = true
		}
	case propVideoDW:
		if hasInt {
			p.snap.VideoWidth = int(*(*int64)(ep.Data))
			newW, newH = p.snap.VideoWidth, p.snap.VideoHeight
			changed = true
		} else if isNone {
			p.snap.VideoWidth = 0
			changed = true
		}
	case propVideoDH:
		if hasInt {
			p.snap.VideoHeight = int(*(*int64)(ep.Data))
			newW, newH = p.snap.VideoWidth, p.snap.VideoHeight
			changed = true
		} else if isNone {
			p.snap.VideoHeight = 0
			changed = true
		}
	case propHWDecCurrent:
		if hasStr {
			p.snap.CurrentHWDec = mpv.GoString(*(*unsafe.Pointer)(ep.Data))
			changed = true
		} else if isNone {
			p.snap.CurrentHWDec = ""
			changed = true
		}
	case propAID:
		if hasStr {
			p.snap.AudioTrackID = parseTrackID(mpv.GoString(*(*unsafe.Pointer)(ep.Data)))
			tracksChanged = true
			changed = true
		} else if isNone {
			p.snap.AudioTrackID = 0
			changed = true
		}
	case propSID:
		if hasStr {
			p.snap.SubTrackID = parseTrackID(mpv.GoString(*(*unsafe.Pointer)(ep.Data)))
			tracksChanged = true
			changed = true
		} else if isNone {
			p.snap.SubTrackID = 0
			changed = true
		}
	case propSubVisibility:
		if hasFlag {
			p.snap.SubVisible = *(*int32)(ep.Data) != 0
			changed = true
		}
	case propTrackListCount:
		tracksChanged = true
		changed = true
	case propMediaTitle:
		if hasStr {
			p.snap.MediaTitle = mpv.GoString(*(*unsafe.Pointer)(ep.Data))
			changed = true
		} else if isNone {
			p.snap.MediaTitle = ""
			changed = true
		}
	case propSubDelay:
		if hasDouble {
			p.snap.SubDelay = durationFromSeconds(*(*float64)(ep.Data))
			changed = true
		}
	case propAudioDelay:
		if hasDouble {
			p.snap.AudioDelay = durationFromSeconds(*(*float64)(ep.Data))
			changed = true
		}
	case propSubScale:
		if hasDouble {
			p.snap.SubScale = *(*float64)(ep.Data)
			changed = true
		}
	case propSubPos:
		if hasDouble {
			p.snap.SubPos = int(math.Round(*(*float64)(ep.Data)))
			changed = true
		}
	case propVideoRotate:
		if hasStr {
			p.snap.VideoRotate = parseRotate(mpv.GoString(*(*unsafe.Pointer)(ep.Data)))
			changed = true
		}
	case propPanscan:
		if hasDouble {
			p.snap.Panscan = *(*float64)(ep.Data)
			changed = true
		}
	}
	p.mu.Unlock()

	if newW > 0 && newH > 0 {
		p.renderer.SetVideoSize(newW, newH)
	}
	if tracksChanged {
		p.refreshTracks()
	}
	if changed {
		p.notifyUI()
	}
	if eofReached && p.opts.OnEndFile != nil && !p.closing.Load() {
		p.opts.OnEndFile(nil)
	}
}

func parseRotate(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (p *Player) recomputeStateLocked() {
	if p.snap.Err != nil {
		p.snap.State = StateError
		return
	}
	if !p.fileLoaded {
		return
	}
	switch {
	case p.snap.EOF:
		p.snap.State = StateEnded
	case p.snap.Buffering:
		p.snap.State = StateBuffering
	case p.snap.Paused:
		p.snap.State = StatePaused
	default:
		p.snap.State = StatePlaying
	}
}

// refreshTracks re-reads track-list into the Snapshot. It never invokes user
// callbacks; callers notify (outside cmdMu) afterwards.
func (p *Player) refreshTracks() {
	raw, err := p.client.GetPropertyNode("track-list")
	if err != nil {
		return
	}
	tracks := parseTrackList(raw)
	var activeAudio, activeSub int64
	for _, tr := range tracks {
		if tr.Selected {
			switch tr.Type {
			case TrackAudio:
				activeAudio = tr.ID
			case TrackSub:
				activeSub = tr.ID
			}
		}
	}
	p.mu.Lock()
	p.snap.Tracks = tracks
	p.snap.AudioTrackID = activeAudio
	p.snap.SubTrackID = activeSub
	p.mu.Unlock()
}

func parseTrackList(raw any) []Track {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	tracks := make([]Track, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t := Track{
			ID:               asInt64(m["id"]),
			Type:             TrackType(asString(m["type"])),
			SrcID:            asInt64(m["src-id"]),
			Title:            asString(m["title"]),
			Lang:             asString(m["lang"]),
			Codec:            asString(m["codec"]),
			Selected:         asBool(m["selected"]),
			Default:          asBool(m["default"]),
			Forced:           asBool(m["forced"]),
			External:         asBool(m["external"]),
			ExternalFilename: asString(m["external-filename"]),
			Width:            int(asInt64(m["demux-w"])),
			Height:           int(asInt64(m["demux-h"])),
			FPS:              asFloat64(m["demux-fps"]),
			Channels:         int(asInt64(m["demux-channel-count"])),
			SampleRate:       int(asInt64(m["demux-samplerate"])),
		}
		tracks = append(tracks, t)
	}
	return tracks
}

func filterTracks(tracks []Track, kind TrackType) []Track {
	var out []Track
	for _, t := range tracks {
		if t.Type == kind {
			out = append(out, t)
		}
	}
	return out
}

func parseTrackID(s string) int64 {
	if s == "" || s == "no" || s == "auto" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func asFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	default:
		return 0
	}
}
