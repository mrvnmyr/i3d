package starlib

import (
	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
)

func EventValue(ev i3ipc.Event) *starlark.Dict {
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("type"), starlark.String(eventTypeName(ev.Type)))
	_ = d.SetKey(starlark.String("change"), starlark.String(ev.Change))
	return d
}

func eventTypeName(t i3ipc.EventType) string {
	switch t {
	case i3ipc.I3WorkspaceEvent:
		return "workspace"
	case i3ipc.I3OutputEvent:
		return "output"
	case i3ipc.I3ModeEvent:
		return "mode"
	case i3ipc.I3WindowEvent:
		return "window"
	case i3ipc.I3BarConfigUpdateEvent:
		return "barconfig_update"
	case i3ipc.I3BindingEvent:
		return "binding"
	default:
		return "unknown"
	}
}
