package mpv

import (
	"fmt"
	"math"
	"unsafe"
)

// Format matches libmpv's mpv_format enum.
type Format int32

const (
	FormatNone      Format = 0
	FormatString    Format = 1
	FormatOSDString Format = 2
	FormatFlag      Format = 3
	FormatInt64     Format = 4
	FormatDouble    Format = 5
	FormatNode      Format = 6
	FormatNodeArray Format = 7
	FormatNodeMap   Format = 8
	FormatByteArray Format = 9
)

// ErrorCode matches libmpv's mpv_error enum.
type ErrorCode int32

const (
	ErrSuccess             ErrorCode = 0
	ErrEventQueueFull      ErrorCode = -1
	ErrNoMem               ErrorCode = -2
	ErrUninitialized       ErrorCode = -3
	ErrInvalidParameter    ErrorCode = -4
	ErrOptionNotFound      ErrorCode = -5
	ErrOptionFormat        ErrorCode = -6
	ErrOptionError         ErrorCode = -7
	ErrPropertyNotFound    ErrorCode = -8
	ErrPropertyFormat      ErrorCode = -9
	ErrPropertyUnavailable ErrorCode = -10
	ErrPropertyError       ErrorCode = -11
	ErrCommand             ErrorCode = -12
	ErrLoadingFailed       ErrorCode = -13
	ErrAOInitFailed        ErrorCode = -14
	ErrVOInitFailed        ErrorCode = -15
	ErrNothingToPlay       ErrorCode = -16
	ErrUnknownFormat       ErrorCode = -17
	ErrUnsupported         ErrorCode = -18
	ErrNotImplemented      ErrorCode = -19
	ErrGeneric             ErrorCode = -20
)

// Error represents an error returned by libmpv.
type Error struct {
	Code ErrorCode
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("mpv %s: %s (%d)", e.Op, e.Msg, e.Code)
	}
	return fmt.Sprintf("mpv: %s (%d)", e.Msg, e.Code)
}

// EventID matches libmpv's mpv_event_id enum.
type EventID int32

const (
	EventNone             EventID = 0
	EventShutdown         EventID = 1
	EventLogMessage       EventID = 2
	EventGetPropertyReply EventID = 3
	EventSetPropertyReply EventID = 4
	EventCommandReply     EventID = 5
	EventStartFile        EventID = 6
	EventEndFile          EventID = 7
	EventFileLoaded       EventID = 8
	EventClientMessage    EventID = 16
	EventVideoReconfig    EventID = 17
	EventAudioReconfig    EventID = 18
	EventSeek             EventID = 20
	EventPlaybackRestart  EventID = 21
	EventPropertyChange   EventID = 22
	EventQueueOverflow    EventID = 24
	EventHook             EventID = 25
)

// EndFileReason matches libmpv's mpv_end_file_reason enum.
type EndFileReason int32

const (
	EndFileEOF      EndFileReason = 0
	EndFileStop     EndFileReason = 2
	EndFileQuit     EndFileReason = 3
	EndFileError    EndFileReason = 4
	EndFileRedirect EndFileReason = 5
)

// RenderParamType matches libmpv's mpv_render_param_type enum.
type RenderParamType int32

const (
	RenderParamInvalid            RenderParamType = 0
	RenderParamAPIType            RenderParamType = 1
	RenderParamOpenGLInitParams   RenderParamType = 2
	RenderParamOpenGLFBO          RenderParamType = 3
	RenderParamFlipY              RenderParamType = 4
	RenderParamDepth              RenderParamType = 5
	RenderParamICCProfile         RenderParamType = 6
	RenderParamAmbientLight       RenderParamType = 7
	RenderParamX11Display         RenderParamType = 8
	RenderParamWLDisplay          RenderParamType = 9
	RenderParamAdvancedControl    RenderParamType = 10
	RenderParamNextFrameInfo      RenderParamType = 11
	RenderParamBlockForTargetTime RenderParamType = 12
	RenderParamSkipRendering      RenderParamType = 13
	RenderParamSWSize             RenderParamType = 17
	RenderParamSWFormat           RenderParamType = 18
	RenderParamSWStride           RenderParamType = 19
	RenderParamSWPointer          RenderParamType = 20
)

// RenderUpdateFrame indicates a new frame should be rendered.
const RenderUpdateFrame uint64 = 1 << 0

// Node matches libmpv's mpv_node (16 bytes on 64-bit platforms).
type Node struct {
	U      uint64
	Format Format
	_      int32
}

func (n Node) Flag() bool       { return int32(n.U) != 0 }
func (n Node) Int64() int64     { return int64(n.U) }
func (n Node) Float64() float64 { return math.Float64frombits(n.U) }
func (n Node) Ptr() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&n.U))
}

// NodeList matches libmpv's mpv_node_list (24 bytes on 64-bit platforms).
type NodeList struct {
	Num    int32
	_      int32
	Values *Node
	Keys   **byte
}

// ByteArray matches libmpv's mpv_byte_array (16 bytes on 64-bit platforms).
type ByteArray struct {
	Data unsafe.Pointer
	Size uintptr
}

// Event matches libmpv's mpv_event (24 bytes on 64-bit platforms).
type Event struct {
	ID            EventID
	Error         ErrorCode
	ReplyUserdata uint64
	Data          unsafe.Pointer
}

// EventProperty matches libmpv's mpv_event_property (24 bytes on 64-bit platforms).
type EventProperty struct {
	Name   unsafe.Pointer
	Format Format
	_      int32
	Data   unsafe.Pointer
}

// EndFileEvent matches libmpv's mpv_event_end_file.
type EndFileEvent struct {
	Reason                   EndFileReason
	Error                    ErrorCode
	PlaylistEntryID          int64
	PlaylistInsertID         int64
	PlaylistInsertNumEntries int32
	_                        int32
}

// LogMessageEvent matches libmpv's mpv_event_log_message.
type LogMessageEvent struct {
	Prefix   unsafe.Pointer
	Level    unsafe.Pointer
	Text     unsafe.Pointer
	LogLevel int32
	_        int32
}

// RenderParam matches libmpv's mpv_render_param (16 bytes on 64-bit platforms).
type RenderParam struct {
	Type RenderParamType
	_    int32
	Data unsafe.Pointer
}

// OpenGLInitParams matches libmpv's mpv_opengl_init_params (16 bytes on 64-bit platforms).
type OpenGLInitParams struct {
	GetProcAddress    uintptr
	GetProcAddressCtx uintptr
}

// OpenGLFBO matches libmpv's mpv_opengl_fbo (16 bytes on 64-bit platforms).
type OpenGLFBO struct {
	FBO            int32
	W              int32
	H              int32
	InternalFormat int32
}
