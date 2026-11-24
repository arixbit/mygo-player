package player

import (
	"errors"
	"fmt"
	"maps"
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
	propCoreIdle
	propVideoDW
	propVideoDH
	propHWDecCurrent
	propAID
	propSID
	propSubVisibility
	propTrackListCount
	propMediaTitle
)

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

	cmdMu   sync.Mutex // serializes open/close/option mutation with mpv
	closing atomic.Bool
	closed  atomic.Bool

	eventDone chan struct{}

	mu         sync.RWMutex
	snap       Snapshot
	fileLoaded bool
	invalidate func()
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
	vol := opt.Volume
	if vol <= 0 && !opt.StartMuted {
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
	obs := []struct {
		id     uint64
		name   string
		format mpv.Format
	}{
		{propPause, "pause", mpv.FormatFlag},
		{propTimePos, "time-pos", mpv.FormatDouble},
		{propDuration, "duration", mpv.FormatDouble},
		{propVolume, "volume", mpv.FormatDouble},
		{propMute, "mute", mpv.FormatFlag},
		{propSpeed, "speed", mpv.FormatDouble},
		{propEOFReached, "eof-reached", mpv.FormatFlag},
		{propPausedForCache, "paused-for-cache", mpv.FormatFlag},
		{propCoreIdle, "core-idle", mpv.FormatFlag},
		{propVideoDW, "video-params/dw", mpv.FormatInt64},
		{propVideoDH, "video-params/dh", mpv.FormatInt64},
		{propHWDecCurrent, "hwdec-current", mpv.FormatString},
		{propAID, "aid", mpv.FormatString},
		{propSID, "sid", mpv.FormatString},
		{propSubVisibility, "sub-visibility", mpv.FormatFlag},
		{propTrackListCount, "track-list/count", mpv.FormatInt64},
		{propMediaTitle, "media-title", mpv.FormatString},
	}
	for _, o := range obs {
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
func (p *Player) Open(target string, opts ...OpenOptions) error {
	if target == "" {
		return errors.New("player: empty media target")
	}
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}

	var opt OpenOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Merge default and per-open HTTP configuration, always resetting previous
	// per-open headers so credentials never leak across opens.
	headers := maps.Clone(p.defaultHeaders)
	if len(opt.Headers) > 0 {
		if headers == nil {
			headers = make(map[string]string, len(opt.Headers))
		}
		maps.Copy(headers, opt.Headers)
	}
	ua := p.defaultUA
	if opt.UserAgent != "" {
		ua = opt.UserAgent
	}
	ref := p.defaultReferrer
	if opt.Referrer != "" {
		ref = opt.Referrer
	}
	if err := p.applyHTTPConfig(headers, ua, ref); err != nil {
		return err
	}

	if err := p.client.SetPropertyFlag("pause", opt.Paused); err != nil {
		return err
	}
	if opt.StartTime > 0 {
		if err := p.client.SetPropertyString("start", strconv.FormatFloat(opt.StartTime.Seconds(), 'f', 3, 64)); err != nil {
			return err
		}
	} else {
		_ = p.client.SetPropertyString("start", "none")
	}
	if len(opt.Subtitles) > 0 {
		if err := p.client.SetPropertyStringList("sub-files", opt.Subtitles); err != nil {
			return err
		}
	} else {
		_ = p.client.SetPropertyStringList("sub-files", nil)
	}

	p.mu.Lock()
	p.fileLoaded = false
	p.snap.State = StateLoading
	p.snap.Err = nil
	p.snap.EOF = false
	p.snap.Position = 0
	p.snap.Duration = 0
	p.snap.Tracks = nil
	p.mu.Unlock()
	p.notifyUI()

	if err := p.client.Command("loadfile", target, "replace"); err != nil {
		p.mu.Lock()
		p.snap.State = StateError
		p.snap.Err = err
		p.mu.Unlock()
		p.notifyUI()
		return err
	}
	return nil
}

// Play starts or resumes playback. If playback reached EOF, it seeks back to 0 first.
func (p *Player) Play() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	p.mu.RLock()
	eof := p.snap.EOF
	p.mu.RUnlock()
	if eof {
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

// TogglePause switches between playing and paused states.
func (p *Player) TogglePause() error {
	if p.Snapshot().Paused {
		return p.Play()
	}
	return p.Pause()
}

// Stop stops playback and returns the player to idle.
func (p *Player) Stop() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	return p.client.Command("stop")
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
	return p.client.Command("seek", strconv.FormatFloat(pos.Seconds(), 'f', 3, 64), "absolute+exact")
}

// SeekRelative seeks forward (positive) or backward (negative) by delta.
func (p *Player) SeekRelative(delta time.Duration) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
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
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if err := p.client.SetPropertyInt64("aid", id); err != nil {
		return err
	}
	p.refreshTracks()
	return nil
}

// DisableAudioTrack disables audio playback ("aid=no").
func (p *Player) DisableAudioTrack() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if err := p.client.SetPropertyString("aid", "no"); err != nil {
		return err
	}
	p.refreshTracks()
	return nil
}

// SelectSubtitleTrack switches to the embedded or external subtitle track with
// the given mpv track ID and ensures subtitle visibility is enabled.
func (p *Player) SelectSubtitleTrack(id int64) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if err := p.client.SetPropertyInt64("sid", id); err != nil {
		return err
	}
	_ = p.client.SetPropertyFlag("sub-visibility", true)
	p.refreshTracks()
	p.renderer.RequestRedraw()
	return nil
}

// DisableSubtitleTrack turns off subtitle track selection ("sid=no").
func (p *Player) DisableSubtitleTrack() error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if err := p.client.SetPropertyString("sid", "no"); err != nil {
		return err
	}
	p.refreshTracks()
	p.renderer.RequestRedraw()
	return nil
}

// SetSubtitleVisible toggles subtitle rendering without changing the selected track.
func (p *Player) SetSubtitleVisible(visible bool) error {
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if err := p.client.SetPropertyFlag("sub-visibility", visible); err != nil {
		return err
	}
	p.renderer.RequestRedraw()
	return nil
}

// AddSubtitleFile attaches an external subtitle file (SRT, ASS/SSA, WebVTT, SUP, etc.).
// By default flag is SubtitleSelect so the added subtitle is immediately active.
func (p *Player) AddSubtitleFile(path string, flags ...SubtitleFlag) error {
	if path == "" {
		return errors.New("player: empty subtitle path")
	}
	p.cmdMu.Lock()
	defer p.cmdMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	flag := SubtitleSelect
	if len(flags) > 0 && flags[0] != "" {
		flag = flags[0]
	}
	if err := p.client.Command("sub-add", path, string(flag)); err != nil {
		return err
	}
	if flag == SubtitleSelect {
		_ = p.client.SetPropertyFlag("sub-visibility", true)
	}
	p.refreshTracks()
	p.renderer.RequestRedraw()
	return nil
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

// Close shuts down the render context, event loop, and libmpv instance cleanly.
func (p *Player) Close() error {
	if p == nil {
		return nil
	}
	if !p.closing.CompareAndSwap(false, true) {
		<-p.eventDone
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

	<-p.eventDone

	p.cmdMu.Lock()
	p.client.TerminateDestroy()
	p.cmdMu.Unlock()
	return nil
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

// splitHTTPHeaders separates standard headers into libmpv's http-header-fields
// list ("Key: Value") and maps User-Agent / Referer into libmpv's dedicated
// properties. Explicit ua / ref parameters take precedence over headers map.
func splitHTTPHeaders(headers map[string]string, ua, ref string) (fields []string, outUA, outRef string) {
	outUA = ua
	outRef = ref
	if len(headers) == 0 {
		return nil, outUA, outRef
	}
	keys := slices.Sorted(maps.Keys(headers))
	for _, k := range keys {
		cleanKey := strings.TrimSpace(k)
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
	defer close(p.eventDone)
	for {
		if p.closing.Load() {
			return
		}
		ev := p.client.WaitEvent(-1)
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
			p.snap.State = StateLoading
			p.snap.Err = nil
			p.snap.EOF = false
			p.mu.Unlock()
			p.notifyUI()
		case mpv.EventFileLoaded:
			p.mu.Lock()
			p.fileLoaded = true
			p.recomputeStateLocked()
			p.mu.Unlock()
			p.refreshTracks()
			p.notifyUI()
		case mpv.EventEndFile:
			var endErr error
			reason := mpv.EndFileEOF
			if ev.Data != nil {
				ef := (*mpv.EndFileEvent)(ev.Data)
				reason = ef.Reason
				if ef.Reason == mpv.EndFileError {
					endErr = &mpv.Error{Code: ef.Error, Op: "playback", Msg: fmt.Sprintf("end file error (%d)", ef.Error)}
				}
			}
			p.mu.Lock()
			switch reason {
			case mpv.EndFileEOF:
				p.snap.EOF = true
				p.snap.State = StateEnded
			case mpv.EndFileError:
				p.snap.Err = endErr
				p.snap.State = StateError
			case mpv.EndFileStop:
				p.fileLoaded = false
				p.snap.State = StateIdle
				p.snap.Tracks = nil
			}
			p.mu.Unlock()
			p.notifyUI()
			if p.opts.OnEndFile != nil {
				p.opts.OnEndFile(endErr)
			}
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

func (p *Player) handlePropertyChange(id uint64, ep *mpv.EventProperty) {
	changed := false
	tracksChanged := false
	var newW, newH int

	p.mu.Lock()
	switch id {
	case propPause:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.snap.Paused = *(*int32)(ep.Data) != 0
			p.recomputeStateLocked()
			changed = true
		}
	case propTimePos:
		if ep.Format == mpv.FormatDouble && ep.Data != nil {
			sec := *(*float64)(ep.Data)
			if sec < 0 {
				sec = 0
			}
			p.snap.Position = time.Duration(sec * float64(time.Second))
			changed = true
		}
	case propDuration:
		if ep.Format == mpv.FormatDouble && ep.Data != nil {
			sec := *(*float64)(ep.Data)
			if sec < 0 {
				sec = 0
			}
			p.snap.Duration = time.Duration(sec * float64(time.Second))
			changed = true
		}
	case propVolume:
		if ep.Format == mpv.FormatDouble && ep.Data != nil {
			p.snap.Volume = *(*float64)(ep.Data)
			changed = true
		}
	case propMute:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.snap.Muted = *(*int32)(ep.Data) != 0
			changed = true
		}
	case propSpeed:
		if ep.Format == mpv.FormatDouble && ep.Data != nil {
			p.snap.Speed = *(*float64)(ep.Data)
			changed = true
		}
	case propEOFReached:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.snap.EOF = *(*int32)(ep.Data) != 0
			p.recomputeStateLocked()
			changed = true
		}
	case propPausedForCache:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.snap.Buffering = *(*int32)(ep.Data) != 0
			p.recomputeStateLocked()
			changed = true
		}
	case propCoreIdle:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.recomputeStateLocked()
			changed = true
		}
	case propVideoDW:
		if ep.Format == mpv.FormatInt64 && ep.Data != nil {
			p.snap.VideoWidth = int(*(*int64)(ep.Data))
			newW, newH = p.snap.VideoWidth, p.snap.VideoHeight
			changed = true
		}
	case propVideoDH:
		if ep.Format == mpv.FormatInt64 && ep.Data != nil {
			p.snap.VideoHeight = int(*(*int64)(ep.Data))
			newW, newH = p.snap.VideoWidth, p.snap.VideoHeight
			changed = true
		}
	case propHWDecCurrent:
		if ep.Format == mpv.FormatString && ep.Data != nil {
			strPtr := *(*unsafe.Pointer)(ep.Data)
			p.snap.CurrentHWDec = mpv.GoString(strPtr)
			changed = true
		} else if ep.Format == mpv.FormatNone {
			p.snap.CurrentHWDec = ""
			changed = true
		}
	case propAID:
		if ep.Format == mpv.FormatString && ep.Data != nil {
			p.snap.AudioTrackID = parseTrackID(mpv.GoString(*(*unsafe.Pointer)(ep.Data)))
			tracksChanged = true
			changed = true
		}
	case propSID:
		if ep.Format == mpv.FormatString && ep.Data != nil {
			p.snap.SubTrackID = parseTrackID(mpv.GoString(*(*unsafe.Pointer)(ep.Data)))
			tracksChanged = true
			changed = true
		}
	case propSubVisibility:
		if ep.Format == mpv.FormatFlag && ep.Data != nil {
			p.snap.SubVisible = *(*int32)(ep.Data) != 0
			changed = true
		}
	case propTrackListCount:
		tracksChanged = true
		changed = true
	case propMediaTitle:
		if ep.Format == mpv.FormatString && ep.Data != nil {
			p.snap.MediaTitle = mpv.GoString(*(*unsafe.Pointer)(ep.Data))
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
	p.notifyUI()
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
