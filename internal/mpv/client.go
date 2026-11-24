package mpv

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/egoist/mygo/ffi"
)

// ErrNilClient is reported (wrapped in *Error with Code ErrUninitialized) when
// a method is invoked on a nil Client or one whose handle was already released
// by TerminateDestroy.
var ErrNilClient = errors.New("mpv: nil or destroyed client")

// Client wraps an mpv_handle. All methods are safe to call on a nil Client or
// after TerminateDestroy: they return an error (or a zero value) instead of
// passing a NULL/freed handle to libmpv.
type Client struct {
	handle atomic.Uintptr
}

// Create allocates a new uninitialized mpv Client.
func Create() (*Client, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	ensureNumericLocale()
	h := fns.Create()
	if h == 0 {
		return nil, errors.New("mpv: mpv_create returned NULL")
	}
	c := &Client{}
	c.handle.Store(h)
	return c, nil
}

// Handle returns the raw mpv_handle pointer (0 for nil or destroyed clients).
func (c *Client) Handle() uintptr {
	if c == nil {
		return 0
	}
	return c.handle.Load()
}

// ptr returns the live handle or a descriptive error for op.
func (c *Client) ptr(op string) (uintptr, error) {
	if c == nil {
		return 0, &Error{Code: ErrUninitialized, Op: op, Msg: ErrNilClient.Error()}
	}
	h := c.handle.Load()
	if h == 0 {
		return 0, &Error{Code: ErrUninitialized, Op: op, Msg: ErrNilClient.Error()}
	}
	return h, nil
}

// Is reports ErrNilClient for nil/destroyed client errors so callers can use
// errors.Is(err, mpv.ErrNilClient).
func (e *Error) Is(target error) bool {
	return target == ErrNilClient && e.Code == ErrUninitialized && e.Msg == ErrNilClient.Error()
}

// Initialize finishes initializing the mpv instance.
func (c *Client) Initialize() error {
	h, err := c.ptr("initialize")
	if err != nil {
		return err
	}
	ensureNumericLocale()
	return checkErr("initialize", fns.Initialize(h))
}

// TerminateDestroy terminates and frees the mpv instance. It is idempotent.
func (c *Client) TerminateDestroy() {
	if c == nil {
		return
	}
	h := c.handle.Swap(0)
	if h == 0 {
		return
	}
	fns.TerminateDestroy(h)
}

// SetOptionString sets an mpv option as a string.
func (c *Client) SetOptionString(name, value string) error {
	op := "set_option_string(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	return checkErr(op, fns.SetOptionString(h, name, value))
}

// SetOptionFlag sets a boolean option on mpv.
func (c *Client) SetOptionFlag(name string, value bool) error {
	op := "set_option(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	var v int32
	if value {
		v = 1
	}
	err = checkErr(op, fns.SetOption(h, name, int32(FormatFlag), unsafe.Pointer(&v)))
	runtime.KeepAlive(&v)
	return err
}

// Command executes an mpv command synchronously.
func (c *Client) Command(args ...string) error {
	h, err := c.ptr("command")
	if err != nil {
		return err
	}
	ptrs, keep := buildCStringArray(args)
	err = checkErr("command", fns.Command(h, &ptrs[0]))
	runtime.KeepAlive(keep)
	runtime.KeepAlive(ptrs)
	return err
}

// CommandAsync executes an mpv command asynchronously.
func (c *Client) CommandAsync(replyUserdata uint64, args ...string) error {
	h, err := c.ptr("command_async")
	if err != nil {
		return err
	}
	ptrs, keep := buildCStringArray(args)
	err = checkErr("command_async", fns.CommandAsync(h, replyUserdata, &ptrs[0]))
	runtime.KeepAlive(keep)
	runtime.KeepAlive(ptrs)
	return err
}

// CommandString runs a raw mpv command string.
func (c *Client) CommandString(cmd string) error {
	h, err := c.ptr("command_string")
	if err != nil {
		return err
	}
	return checkErr("command_string", fns.CommandString(h, cmd))
}

// SetPropertyString sets a property as a string.
func (c *Client) SetPropertyString(name, value string) error {
	op := "set_property_string(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	return checkErr(op, fns.SetPropertyStr(h, name, value))
}

// SetPropertyFlag sets a boolean property.
func (c *Client) SetPropertyFlag(name string, value bool) error {
	op := "set_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	var v int32
	if value {
		v = 1
	}
	err = checkErr(op, fns.SetProperty(h, name, int32(FormatFlag), unsafe.Pointer(&v)))
	runtime.KeepAlive(&v)
	return err
}

// SetPropertyInt64 sets an int64 property.
func (c *Client) SetPropertyInt64(name string, value int64) error {
	op := "set_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	v := value
	err = checkErr(op, fns.SetProperty(h, name, int32(FormatInt64), unsafe.Pointer(&v)))
	runtime.KeepAlive(&v)
	return err
}

// SetPropertyFloat64 sets a float64 property.
func (c *Client) SetPropertyFloat64(name string, value float64) error {
	op := "set_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	v := value
	err = checkErr(op, fns.SetProperty(h, name, int32(FormatDouble), unsafe.Pointer(&v)))
	runtime.KeepAlive(&v)
	return err
}

// SetPropertyStringList sets a string-list property (such as http-header-fields)
// using an MPV_FORMAT_NODE array so values containing commas or quotes need no
// string escaping.
func (c *Client) SetPropertyStringList(name string, items []string) error {
	op := "set_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	bufs := make([][]byte, len(items))
	nodes := make([]Node, len(items))
	for i, s := range items {
		b := make([]byte, len(s)+1)
		copy(b, s)
		bufs[i] = b
		nodes[i] = Node{
			U:      uint64(uintptr(unsafe.Pointer(&b[0]))),
			Format: FormatString,
		}
	}
	var valuesPtr *Node
	if len(nodes) > 0 {
		valuesPtr = &nodes[0]
	}
	list := NodeList{
		Num:    int32(len(nodes)),
		Values: valuesPtr,
	}
	root := Node{
		U:      uint64(uintptr(unsafe.Pointer(&list))),
		Format: FormatNodeArray,
	}
	err = checkErr(op, fns.SetProperty(h, name, int32(FormatNode), unsafe.Pointer(&root)))
	runtime.KeepAlive(bufs)
	runtime.KeepAlive(nodes)
	runtime.KeepAlive(&list)
	runtime.KeepAlive(&root)
	return err
}

// GetPropertyString reads a property as a string.
func (c *Client) GetPropertyString(name string) (string, error) {
	op := "get_property_string(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return "", err
	}
	p := fns.GetPropertyStr(h, name)
	if p == nil {
		return "", &Error{Code: ErrPropertyUnavailable, Op: op, Msg: "property unavailable"}
	}
	defer fns.Free(p)
	return ffi.GoString(p), nil
}

// GetPropertyFlag reads a boolean property.
func (c *Client) GetPropertyFlag(name string) (bool, error) {
	op := "get_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return false, err
	}
	var v int32
	if err := checkErr(op, fns.GetProperty(h, name, int32(FormatFlag), unsafe.Pointer(&v))); err != nil {
		return false, err
	}
	return v != 0, nil
}

// GetPropertyInt64 reads an int64 property.
func (c *Client) GetPropertyInt64(name string) (int64, error) {
	op := "get_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return 0, err
	}
	var v int64
	if err := checkErr(op, fns.GetProperty(h, name, int32(FormatInt64), unsafe.Pointer(&v))); err != nil {
		return 0, err
	}
	return v, nil
}

// GetPropertyFloat64 reads a float64 property.
func (c *Client) GetPropertyFloat64(name string) (float64, error) {
	op := "get_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return 0, err
	}
	var v float64
	if err := checkErr(op, fns.GetProperty(h, name, int32(FormatDouble), unsafe.Pointer(&v))); err != nil {
		return 0, err
	}
	return v, nil
}

// GetPropertyNode reads a structured property (such as track-list) and decodes
// it into native Go values (nil, bool, int64, float64, string, []any,
// map[string]any, []byte).
func (c *Client) GetPropertyNode(name string) (any, error) {
	op := "get_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return nil, err
	}
	var node Node
	if err := checkErr(op, fns.GetProperty(h, name, int32(FormatNode), unsafe.Pointer(&node))); err != nil {
		return nil, err
	}
	defer fns.FreeNodeContents(&node)
	return decodeNode(node), nil
}

// ObserveProperty requests property change events for name.
func (c *Client) ObserveProperty(userdata uint64, name string, format Format) error {
	op := "observe_property(" + name + ")"
	h, err := c.ptr(op)
	if err != nil {
		return err
	}
	return checkErr(op, fns.ObserveProperty(h, userdata, name, int32(format)))
}

// UnobserveProperty stops observing properties registered with userdata.
func (c *Client) UnobserveProperty(userdata uint64) error {
	h, err := c.ptr("unobserve_property")
	if err != nil {
		return err
	}
	return checkErr("unobserve_property", fns.UnobserveProp(h, userdata))
}

// RequestLogMessages sets the minimum log level ("no", "fatal", "error", "warn", "info", "v", "debug", "trace").
func (c *Client) RequestLogMessages(minLevel string) error {
	h, err := c.ptr("request_log_messages")
	if err != nil {
		return err
	}
	return checkErr("request_log_messages", fns.RequestLogMsgs(h, minLevel))
}

// WaitEvent waits for the next mpv event. timeout < 0 waits indefinitely, 0 polls.
// It returns nil for nil or destroyed clients.
func (c *Client) WaitEvent(timeout float64) *Event {
	h, err := c.ptr("wait_event")
	if err != nil {
		return nil
	}
	p := fns.WaitEvent(h, timeout)
	if p == nil {
		return nil
	}
	return (*Event)(p)
}

// Wakeup interrupts an active WaitEvent call.
func (c *Client) Wakeup() {
	h, err := c.ptr("wakeup")
	if err != nil {
		return
	}
	fns.Wakeup(h)
}

// GoString copies a NUL-terminated C string from ptr.
func GoString(ptr unsafe.Pointer) string {
	return ffi.GoString(ptr)
}

// decodeNode recursively converts a libmpv mpv_node into Go values before
// mpv_free_node_contents releases the underlying C memory.
func decodeNode(n Node) any {
	switch n.Format {
	case FormatNone:
		return nil
	case FormatString, FormatOSDString:
		return ffi.GoString(n.Ptr())
	case FormatFlag:
		return n.Flag()
	case FormatInt64:
		return n.Int64()
	case FormatDouble:
		return n.Float64()
	case FormatNodeArray:
		p := n.Ptr()
		if p == nil {
			return []any(nil)
		}
		list := (*NodeList)(p)
		if list.Num <= 0 || list.Values == nil {
			return []any{}
		}
		vals := unsafe.Slice(list.Values, int(list.Num))
		out := make([]any, len(vals))
		for i, v := range vals {
			out[i] = decodeNode(v)
		}
		return out
	case FormatNodeMap:
		p := n.Ptr()
		if p == nil {
			return map[string]any(nil)
		}
		list := (*NodeList)(p)
		if list.Num <= 0 || list.Values == nil || list.Keys == nil {
			return map[string]any{}
		}
		vals := unsafe.Slice(list.Values, int(list.Num))
		keys := unsafe.Slice(list.Keys, int(list.Num))
		out := make(map[string]any, len(vals))
		for i, v := range vals {
			k := ffi.GoString(unsafe.Pointer(keys[i]))
			out[k] = decodeNode(v)
		}
		return out
	case FormatByteArray:
		p := n.Ptr()
		if p == nil {
			return []byte(nil)
		}
		ba := (*ByteArray)(p)
		if ba.Data == nil || ba.Size == 0 {
			return []byte{}
		}
		src := unsafe.Slice((*byte)(ba.Data), int(ba.Size))
		dst := make([]byte, len(src))
		copy(dst, src)
		return dst
	default:
		return nil
	}
}

func buildCStringArray(args []string) ([]*byte, [][]byte) {
	bufs := make([][]byte, len(args))
	ptrs := make([]*byte, len(args)+1)
	for i, a := range args {
		b := make([]byte, len(a)+1)
		copy(b, a)
		bufs[i] = b
		ptrs[i] = &b[0]
	}
	ptrs[len(args)] = nil
	return ptrs, bufs
}

// RenderContext wraps an mpv_render_context.
type RenderContext struct {
	handle uintptr
	cbID   uint64
}

var (
	renderCBOnce   sync.Once
	renderCBAddr   uintptr
	renderCBNextID atomic.Uint64
	renderCBMap    sync.Map // uint64 -> func()
)

func renderUpdateTrampoline() uintptr {
	renderCBOnce.Do(func() {
		renderCBAddr = newRenderUpdateCallback(func(ctx uintptr) uintptr {
			if v, ok := renderCBMap.Load(uint64(ctx)); ok {
				if fn, ok := v.(func()); ok && fn != nil {
					fn()
				}
			}
			return 0
		})
	})
	return renderCBAddr
}

// NewRenderContext creates an mpv_render_context for client with params.
// params must end with a RenderParam{Type: RenderParamInvalid} sentinel.
func NewRenderContext(client *Client, params []RenderParam) (*RenderContext, error) {
	h, err := client.ptr("render_context_create")
	if err != nil {
		return nil, err
	}
	var res uintptr
	err = checkErr("render_context_create", fns.RenderContextCreate(&res, h, &params[0]))
	runtime.KeepAlive(params)
	if err != nil {
		return nil, err
	}
	return &RenderContext{handle: res}, nil
}

// SetUpdateCallback registers a thread-safe notification callback invoked when
// libmpv has a new video frame ready to render. The callback must not call any
// libmpv functions directly.
func (r *RenderContext) SetUpdateCallback(fn func()) {
	if r == nil || r.handle == 0 {
		return
	}
	if r.cbID != 0 {
		renderCBMap.Delete(r.cbID)
		r.cbID = 0
	}
	if fn == nil {
		fns.RenderContextSetUpdateCallback(r.handle, 0, 0)
		return
	}
	id := renderCBNextID.Add(1)
	r.cbID = id
	renderCBMap.Store(id, fn)
	fns.RenderContextSetUpdateCallback(r.handle, renderUpdateTrampoline(), uintptr(id))
}

// Update queries whether a new frame needs to be rendered.
func (r *RenderContext) Update() uint64 {
	if r == nil || r.handle == 0 {
		return 0
	}
	return fns.RenderContextUpdate(r.handle)
}

// Render renders a frame according to params (terminated by RenderParamInvalid).
func (r *RenderContext) Render(params []RenderParam) error {
	if r == nil || r.handle == 0 {
		return errors.New("mpv: render context closed")
	}
	err := checkErr("render_context_render", fns.RenderContextRender(r.handle, &params[0]))
	runtime.KeepAlive(params)
	return err
}

// ReportSwap notifies libmpv that the rendered frame has been presented.
func (r *RenderContext) ReportSwap() {
	if r == nil || r.handle == 0 {
		return
	}
	fns.RenderContextReportSwap(r.handle)
}

// Free releases the render context. Must be called on the OpenGL context thread
// before Client.TerminateDestroy.
func (r *RenderContext) Free() {
	if r == nil || r.handle == 0 {
		return
	}
	h := r.handle
	r.handle = 0
	if r.cbID != 0 {
		fns.RenderContextSetUpdateCallback(h, 0, 0)
		renderCBMap.Delete(r.cbID)
		r.cbID = 0
	}
	fns.RenderContextFree(h)
}
