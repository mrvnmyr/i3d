package starlib

import (
	"testing"

	"go.starlark.net/starlark"
)

func TestEnrichWindowEventUsesEventContainer(t *testing.T) {
	tree := makeSyntheticTree(2, 1)
	rt := newBenchRuntimeWithTree(tree)
	ev := EventValue("window", "fullscreen_mode")

	const eventConID = int64(200001)
	const eventFullscreenMode = int64(2)
	if err := rt.EnrichWindowEvent(ev, eventConID, eventFullscreenMode); err != nil {
		t.Fatalf("EnrichWindowEvent: %v", err)
	}

	assertDictInt(t, ev, "con_id", eventConID)
	assertDictInt(t, ev, "workspace_num", 2)
	assertDictInt(t, ev, "fullscreen_mode", eventFullscreenMode)
}

func TestEnrichWindowEventPreservesMissingContainerIdentity(t *testing.T) {
	rt := newBenchRuntimeWithTree(makeSyntheticTree(1, 1))
	ev := EventValue("window", "close")

	const eventConID = int64(999999)
	if err := rt.EnrichWindowEvent(ev, eventConID, 0); err != nil {
		t.Fatalf("EnrichWindowEvent: %v", err)
	}

	assertDictInt(t, ev, "con_id", eventConID)
	assertDictInt(t, ev, "fullscreen_mode", 0)
	got, found, err := ev.Get(starlark.String("workspace_num"))
	if err != nil || !found {
		t.Fatalf("workspace_num lookup: found=%v err=%v", found, err)
	}
	if got != starlark.None {
		t.Fatalf("workspace_num=%v want None", got)
	}
}

func TestMessageTypeValues(t *testing.T) {
	tests := []struct {
		got  messageType
		want uint32
	}{
		{messageTypeRunCommand, 0},
		{messageTypeGetWorkspaces, 1},
		{messageTypeSubscribe, 2},
		{messageTypeGetOutputs, 3},
		{messageTypeGetTree, 4},
		{messageTypeGetMarks, 5},
		{messageTypeGetBarConfig, 6},
		{messageTypeGetVersion, 7},
	}
	for _, tt := range tests {
		if uint32(tt.got) != tt.want {
			t.Fatalf("message type=%d want=%d", tt.got, tt.want)
		}
	}
}

func assertDictInt(t *testing.T, d *starlark.Dict, key string, want int64) {
	t.Helper()
	v, found, err := d.Get(starlark.String(key))
	if err != nil || !found {
		t.Fatalf("%s lookup: found=%v err=%v", key, found, err)
	}
	i, ok := v.(starlark.Int)
	if !ok {
		t.Fatalf("%s has type %s, want int", key, v.Type())
	}
	got, ok := i.Int64()
	if !ok || got != want {
		t.Fatalf("%s=%v want=%d", key, v, want)
	}
}
