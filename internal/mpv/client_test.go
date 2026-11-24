package mpv

import (
	"testing"
	"unsafe"
)

func TestABIStructLayouts(t *testing.T) {
	if got := unsafe.Sizeof(Node{}); got != 16 {
		t.Fatalf("sizeof(Node) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(Node{}.Format); got != 8 {
		t.Fatalf("offsetof(Node.Format) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(NodeList{}); got != 24 {
		t.Fatalf("sizeof(NodeList) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(NodeList{}.Values); got != 8 {
		t.Fatalf("offsetof(NodeList.Values) = %d, want 8", got)
	}
	if got := unsafe.Offsetof(NodeList{}.Keys); got != 16 {
		t.Fatalf("offsetof(NodeList.Keys) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(Event{}); got != 24 {
		t.Fatalf("sizeof(Event) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(Event{}.Data); got != 16 {
		t.Fatalf("offsetof(Event.Data) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(EventProperty{}); got != 24 {
		t.Fatalf("sizeof(EventProperty) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(EventProperty{}.Data); got != 16 {
		t.Fatalf("offsetof(EventProperty.Data) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(RenderParam{}); got != 16 {
		t.Fatalf("sizeof(RenderParam) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(RenderParam{}.Data); got != 8 {
		t.Fatalf("offsetof(RenderParam.Data) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(OpenGLInitParams{}); got != 16 {
		t.Fatalf("sizeof(OpenGLInitParams) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(OpenGLFBO{}); got != 16 {
		t.Fatalf("sizeof(OpenGLFBO) = %d, want 16", got)
	}
}
