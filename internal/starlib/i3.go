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
		"find":           starlark.NewBuiltin("i3.find", rt.builtinI3Find),
		"set_urgency":    starlark.NewBuiltin("i3.set_urgency", rt.builtinI3SetUrgency),
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

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.command cmd=%q", cmd)
	}

	ok, err := rt.i3.Command(cmd)

	if rt.debug && rt.debugf != nil {
		if err != nil {
			rt.debugf("i3.command ok=%v err=%v", ok, err)
		} else {
			rt.debugf("i3.command ok=%v", ok)
		}
	}

	if err != nil {
		return nil, err
	}
	return starlark.Bool(ok), nil
}

func (rt *Runtime) builtinI3SetUrgency(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var conID starlark.Int
	urgent := true

	if err := starlark.UnpackArgs(
		b.Name(), args, kwargs,
		"con_id", &conID,
		"urgent?", &urgent,
	); err != nil {
		return nil, err
	}

	cid, ok := conID.Int64()
	if !ok {
		return nil, fmt.Errorf("con_id out of range")
	}
	if cid <= 0 {
		return nil, fmt.Errorf("con_id must be > 0")
	}

	mode := "enable"
	if !urgent {
		mode = "disable"
	}
	cmd := fmt.Sprintf(`[con_id="%d"] urgent %s`, cid, mode)

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.set_urgency con_id=%d urgent=%v cmd=%q", cid, urgent, cmd)
	}

	ok2, err := rt.i3.Command(cmd)

	if rt.debug && rt.debugf != nil {
		if err != nil {
			rt.debugf("i3.set_urgency ok=%v err=%v", ok2, err)
		} else {
			rt.debugf("i3.set_urgency ok=%v", ok2)
		}
	}

	if err != nil {
		return nil, err
	}
	return starlark.Bool(ok2), nil
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

	// Per-dispatch cache for GET_TREE.
	if mt == i3ipc.I3GetTree {
		if rt.debug && rt.debugf != nil {
			rt.debugf("i3.raw get_tree (payload ignored, len=%d)", len(payload))
		}
		raw, err := rt.getTreeRaw()
		if err != nil {
			return nil, err
		}
		return starlark.String(string(raw)), nil
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

	// Per-dispatch cache for GET_TREE.
	if mt == i3ipc.I3GetTree {
		if rt.debug && rt.debugf != nil {
			rt.debugf("i3.query get_tree (payload ignored, len=%d)", len(payload))
		}
		return rt.getTreeStarlark()
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

func (rt *Runtime) builtinI3Find(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var critVal starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "criteria", &critVal); err != nil {
		return nil, err
	}

	cm, ok := critVal.(starlark.IterableMapping)
	if !ok {
		return nil, fmt.Errorf("criteria must be a dict-like mapping, got %s", critVal.Type())
	}

	// Read criteria keys into a Go map.
	opts := map[string]starlark.Value{}
	iter := cm.Iterate()
	defer iter.Done()
	var k starlark.Value
	for iter.Next(&k) {
		ks, ok := starlark.AsString(k)
		if !ok {
			return nil, fmt.Errorf("criteria keys must be strings, got %s", k.Type())
		}
		v, found, err := cm.Get(k)
		if err != nil {
			return nil, fmt.Errorf("criteria[%s]: %w", ks, err)
		}
		if !found {
			continue
		}
		opts[ks] = v
	}

	fields := []string{"id"}
	if fv, ok := opts["fields"]; ok {
		fs, err := toStringSliceValue(fv)
		if err != nil {
			return nil, fmt.Errorf("fields: %w", err)
		}
		if len(fs) > 0 {
			fields = fs
		}
		delete(opts, "fields")
	}

	limit := 0
	if lv, ok := opts["limit"]; ok {
		iv, ok2 := lv.(starlark.Int)
		if !ok2 {
			return nil, fmt.Errorf("limit must be int, got %s", lv.Type())
		}
		i64, ok3 := iv.Int64()
		if !ok3 {
			return nil, fmt.Errorf("limit out of range")
		}
		if i64 < 0 {
			return nil, fmt.Errorf("limit must be >= 0")
		}
		limit = int(i64)
		delete(opts, "limit")
	}

	match := map[string]any{}

	// Optional: where={...} dict.
	if wv, ok := opts["where"]; ok {
		wm, ok2 := wv.(starlark.IterableMapping)
		if !ok2 {
			return nil, fmt.Errorf("where must be a dict-like mapping, got %s", wv.Type())
		}
		witer := wm.Iterate()
		defer witer.Done()
		var wk starlark.Value
		for witer.Next(&wk) {
			wks, ok := starlark.AsString(wk)
			if !ok {
				return nil, fmt.Errorf("where keys must be strings, got %s", wk.Type())
			}
			val, found, err := wm.Get(wk)
			if err != nil {
				return nil, fmt.Errorf("where[%s]: %w", wks, err)
			}
			if !found {
				continue
			}
			nv, err := normalizeScalar(val)
			if err != nil {
				return nil, fmt.Errorf("where[%s]: %w", wks, err)
			}
			match[wks] = nv
		}
		delete(opts, "where")
	}

	// Remaining top-level keys are treated as matchers.
	for key, val := range opts {
		nv, err := normalizeScalar(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		match[key] = nv
	}

	crit := FindCriteria{
		Match:  match,
		Fields: fields,
		Limit:  limit,
	}

	res, err := rt.findInTree(crit)
	if err != nil {
		return nil, err
	}

	out := make([]starlark.Value, 0, len(res))
	for _, m := range res {
		sv, err := JSONToStarlark(m)
		if err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return starlark.NewList(out), nil
}

func (rt *Runtime) builtinI3GetTree(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	_ = thread
	_ = b
	if len(args) != 0 || len(kwargs) != 0 {
		return nil, fmt.Errorf("get_tree takes no arguments")
	}
	return rt.getTreeStarlark()
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

func toStringSliceValue(v starlark.Value) ([]string, error) {
	it, ok := v.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("expected iterable (list/tuple), got %s", v.Type())
	}
	iter := it.Iterate()
	defer iter.Done()

	var out []string
	var item starlark.Value
	for iter.Next(&item) {
		s, ok := starlark.AsString(item)
		if !ok {
			return nil, fmt.Errorf("expected string, got %s", item.Type())
		}
		out = append(out, s)
	}
	return out, nil
}

func normalizeScalar(v starlark.Value) (any, error) {
	switch x := v.(type) {
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		return string(x), nil
	case starlark.Int:
		i64, ok := x.Int64()
		if !ok {
			return nil, fmt.Errorf("int out of range")
		}
		return i64, nil
	case starlark.NoneType:
		return nil, nil
	default:
		// Allow callers to pass simple strings/bools/ints only (fast + predictable).
		return nil, fmt.Errorf("unsupported value type %s (want bool/string/int/None)", v.Type())
	}
}
