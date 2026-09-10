package daemon

import (
	"testing"

	i3 "go.i3wm.org/i3/v4"
)

func TestAdaptWindowEventPreservesContainer(t *testing.T) {
	want := &i3.WindowEvent{
		Change: "fullscreen_mode",
		Container: i3.Node{
			ID:             i3.NodeID(42),
			FullscreenMode: i3.FullscreenGlobal,
		},
	}

	got, ok := adaptEvent(want)
	if !ok {
		t.Fatal("adaptEvent rejected a window event")
	}
	if got.Type != i3.WindowEventType {
		t.Fatalf("type=%q want=%q", got.Type, i3.WindowEventType)
	}
	if got.Change != want.Change {
		t.Fatalf("change=%q want=%q", got.Change, want.Change)
	}
	if got.Window != want {
		t.Fatal("adaptEvent did not preserve the window event payload")
	}
}

func TestAdaptEventTypes(t *testing.T) {
	tests := []struct {
		name   string
		event  i3.Event
		typ    i3.EventType
		change string
	}{
		{"workspace", &i3.WorkspaceEvent{Change: "focus"}, i3.WorkspaceEventType, "focus"},
		{"output", &i3.OutputEvent{Change: "unspecified"}, i3.OutputEventType, "unspecified"},
		{"mode", &i3.ModeEvent{Change: "resize"}, i3.ModeEventType, "resize"},
		{"barconfig", &i3.BarconfigUpdateEvent{}, i3.BarconfigUpdateEventType, ""},
		{"binding", &i3.BindingEvent{Change: "run"}, i3.BindingEventType, "run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := adaptEvent(tt.event)
			if !ok {
				t.Fatal("adaptEvent rejected a subscribed event")
			}
			if got.Type != tt.typ || got.Change != tt.change {
				t.Fatalf("got type=%q change=%q, want type=%q change=%q", got.Type, got.Change, tt.typ, tt.change)
			}
		})
	}
}
