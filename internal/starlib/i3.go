package starlib

import (
	"encoding/json"
	"fmt"

	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
)

func (rt *Runtime) i3Attrs() starlark.StringDict {
	return starlark.StringDict{
		"command":        starlark.NewBuiltin("i3.command", rt.builtinI3Command),
		"raw":            starlark.NewBuiltin("i3.raw", rt.builtinI3Raw),
		"query":          starlark.NewBuiltin("i3.query", rt.builtinI3Query),
		"get_tree":       starlark.NewBuiltin("i3.get_tree", rt.builtinI3GetTree),
		"get_workspaces": starlark.NewBuiltin("i3.get_workspaces", rt.builtinI3GetWorkspaces),
		"get_outputs":    starlark.NewBuiltin("i3.get_outputs", rt.builtinI3GetOutputs),
		"get_marks":      starlark.NewBuiltin("i3.get_marks", rt.builtinI3GetMarks),
		"get_version":    starlark.NewBuiltin("i3.get_version", rt.builtinI3GetVersion),
		"get_bar_ids":    starlark.NewBuiltin("i3.get_bar_ids", rt.builtinI3GetBarIDs),
		"get_bar_config": starlark.NewBuiltin("i3.get_bar_config", rt.builtinI3GetBarConfig),
	}
}

func (rt *Runtime) builtinI3Command(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var cmd string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "cmd", &cmd); err != nil {
		return nil, err
	}
	ok, err := rt.i3.Command(cmd)
	if err != nil {
		return nil, err
	}
	return starlark.Bool(ok), nil
}

func (rt *Runtime) builtinI3Raw(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msg string
	var payload string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "msg", &msg, "payload?", &payload); err != nil {
		return nil, err
	}

	if isBarIDsMsg(msg) {
		if rt.debug && rt.debugf != nil {
			rt.debugf("i3.raw get_bar_ids (payload ignored, len=%d)", len(payload))
		}
		ids, err := rt.i3.GetBarIds()
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(ids)
		if err != nil {
			return nil, fmt.Errorf("json encode: %w", err)
		}
		return starlark.String(string(raw)), nil
	}

	mt, err := parseMessageType(msg)
	if err != nil {
		return nil, err
	}

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.raw msg=%q payload_len=%d", msg, len(payload))
	}

	raw, err := rt.i3.Raw(mt, payload)
	if err != nil {
		return nil, err
	}
	return starlark.String(string(raw)), nil
}

func (rt *Runtime) builtinI3Query(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msg string
	var payload string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "msg", &msg, "payload?", &payload); err != nil {
		return nil, err
	}

	if isBarIDsMsg(msg) {
		if rt.debug && rt.debugf != nil {
			rt.debugf("i3.query get_bar_ids (payload ignored, len=%d)", len(payload))
		}
		ids, err := rt.i3.GetBarIds()
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(ids))
		for _, id := range ids {
			out = append(out, starlark.String(id))
		}
		return starlark.NewList(out), nil
	}

	mt, err := parseMessageType(msg)
	if err != nil {
		return nil, err
	}

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.query msg=%q payload_len=%d", msg, len(payload))
	}

	raw, err := rt.i3.Raw(mt, payload)
	if err != nil {
		return nil, err
	}

	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func (rt *Runtime) builtinI3GetTree(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = thread
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_tree takes no arguments")
	}
	raw, err := rt.i3.Raw(i3ipc.I3GetTree, "")
	if err != nil {
		return nil, err
	}
	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func (rt *Runtime) builtinI3GetWorkspaces(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_workspaces takes no arguments")
	}
	raw, err := rt.i3.Raw(i3ipc.I3GetWorkspaces, "")
	if err != nil {
		return nil, err
	}
	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func (rt *Runtime) builtinI3GetOutputs(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_outputs takes no arguments")
	}
	raw, err := rt.i3.Raw(i3ipc.I3GetOutputs, "")
	if err != nil {
		return nil, err
	}
	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func (rt *Runtime) builtinI3GetMarks(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_marks takes no arguments")
	}
	marks, err := rt.i3.GetMarks()
	if err != nil {
		return nil, err
	}
	out := make([]starlark.Value, 0, len(marks))
	for _, m := range marks {
		out = append(out, starlark.String(m))
	}
	return starlark.NewList(out), nil
}

func (rt *Runtime) builtinI3GetVersion(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_version takes no arguments")
	}
	raw, err := rt.i3.Raw(i3ipc.I3GetVersion, "")
	if err != nil {
		return nil, err
	}
	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func (rt *Runtime) builtinI3GetBarIDs(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_bar_ids takes no arguments")
	}
	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.get_bar_ids")
	}
	ids, err := rt.i3.GetBarIds()
	if err != nil {
		return nil, err
	}
	out := make([]starlark.Value, 0, len(ids))
	for _, id := range ids {
		out = append(out, starlark.String(id))
	}
	return starlark.NewList(out), nil
}

func (rt *Runtime) builtinI3GetBarConfig(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = b
	var id string
	if err := starlark.UnpackArgs("get_bar_config", args, kwargs, "bar_id", &id); err != nil {
		return nil, err
	}
	raw, err := rt.i3.Raw(i3ipc.I3GetBarConfig, id)
	if err != nil {
		return nil, err
	}
	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return JSONToStarlark(anyv)
}

func parseMessageType(s string) (i3ipc.MessageType, error) {
	switch lower(s) {
	case "get_tree", "tree":
		return i3ipc.I3GetTree, nil
	case "get_workspaces", "workspaces":
		return i3ipc.I3GetWorkspaces, nil
	case "get_outputs", "outputs":
		return i3ipc.I3GetOutputs, nil
	case "get_marks", "marks":
		return i3ipc.I3GetMarks, nil
	case "get_bar_config", "bar_config", "get_barconfig":
		return i3ipc.I3GetBarConfig, nil
	case "get_version", "version":
		return i3ipc.I3GetVersion, nil
	default:
		return 0, fmt.Errorf("unknown i3 message type: %q", s)
	}
}

func isBarIDsMsg(s string) bool {
	switch lower(s) {
	case "get_bar_ids", "bar_ids", "get_barids":
		return true
	default:
		return false
	}
}
