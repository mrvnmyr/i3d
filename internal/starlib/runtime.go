package starlib

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
)

type treeCache struct {
	raw []byte
	any any
	st  starlark.Value
}

type FindCriteria struct {
	// Match is a set of simple equality matchers: bool/string/int64/nil.
	Match map[string]any
	// Fields selects which fields to return per match.
	Fields []string
	// Limit caps results; 0 means unlimited.
	Limit int
}

type Runtime struct {
	i3    *i3ipc.IPCSocket
	exec  *ExecRunner
	debug bool

	debugf func(string, ...any)
	logf   func(string, ...any)

	i3mod starlark.Value

	// Per-dispatch caches (cleared via BeginEvent/EndEvent).
	eventTree *treeCache
}

func NewRuntime(i3 *i3ipc.IPCSocket, exec *ExecRunner, debug bool, debugf func(string, ...any), logf func(string, ...any)) *Runtime {
	rt := &Runtime{
		i3:     i3,
		exec:   exec,
		debug:  debug,
		debugf: debugf,
		logf:   logf,
	}
	rt.i3mod = newModule("i3", rt.i3Attrs())
	return rt
}

// BeginEvent resets per-dispatch caches (e.g. GET_TREE) for a single i3 event dispatch.
func (rt *Runtime) BeginEvent() { rt.eventTree = nil }

// EndEvent clears per-dispatch caches. (Symmetric with BeginEvent.)
func (rt *Runtime) EndEvent() { rt.eventTree = nil }

func (rt *Runtime) NewThread(scriptPath string) *starlark.Thread {
	base := filepath.Base(scriptPath)
	return &starlark.Thread{
		Name: base,
		Print: func(_ *starlark.Thread, msg string) {
			// Script print(...) always emits; keep it simple and fast.
			fmt.Fprintln(os.Stdout, msg)
		},
	}
}

func (rt *Runtime) Predeclared(scriptPath string) starlark.StringDict {
	pre := starlark.StringDict{
		"i3":      rt.i3mod,
		"exec":    starlark.NewBuiltin("exec", rt.builtinExec),
		"log":     starlark.NewBuiltin("log", rt.builtinLog),
		"debug":   starlark.Bool(rt.debug),
		"__file__": starlark.String(scriptPath),
	}
	return pre
}

type Handler interface {
	ThreadValue() *starlark.Thread
	CallableValue() starlark.Callable
}

func (rt *Runtime) CallHandler(h Handler, event starlark.Value) error {
	return rt.CallHandler2(h.ThreadValue(), h.CallableValue(), event)
}

// CallHandler2 executes a callable on a specific thread.
func (rt *Runtime) CallHandler2(thread *starlark.Thread, fn starlark.Callable, ev starlark.Value) error {
	_, err := starlark.Call(thread, fn, starlark.Tuple{ev}, nil)
	return err
}

func (rt *Runtime) builtinLog(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msg string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "msg", &msg); err != nil {
		return nil, err
	}
	rt.logf("%s", msg)
	return starlark.None, nil
}

func (rt *Runtime) getTreeRaw() ([]byte, error) {
	if rt.eventTree != nil && rt.eventTree.raw != nil {
		return rt.eventTree.raw, nil
	}

	raw, err := rt.i3.Raw(i3ipc.I3GetTree, "")
	if err != nil {
		return nil, err
	}

	if rt.debug && rt.debugf != nil {
		rt.debugf("GET_TREE fetched bytes=%d", len(raw))
	}

	if rt.eventTree == nil {
		rt.eventTree = &treeCache{}
	}
	rt.eventTree.raw = raw
	return raw, nil
}

func (rt *Runtime) getTreeAny() (any, error) {
	if rt.eventTree != nil && rt.eventTree.any != nil {
		return rt.eventTree.any, nil
	}

	raw, err := rt.getTreeRaw()
	if err != nil {
		return nil, err
	}

	var anyv any
	if err := json.Unmarshal(raw, &anyv); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}

	if rt.eventTree == nil {
		rt.eventTree = &treeCache{}
	}
	rt.eventTree.any = anyv
	return anyv, nil
}

func (rt *Runtime) getTreeStarlark() (starlark.Value, error) {
	if rt.eventTree != nil && rt.eventTree.st != nil {
		return rt.eventTree.st, nil
	}

	anyv, err := rt.getTreeAny()
	if err != nil {
		return nil, err
	}

	sv, err := JSONToStarlark(anyv)
	if err != nil {
		return nil, err
	}

	if rt.eventTree == nil {
		rt.eventTree = &treeCache{}
	}
	rt.eventTree.st = sv
	return sv, nil
}

// EnrichWindowEvent adds:
//   con_id: int|None
//   workspace_num: int|None
//   fullscreen_mode: int|None
// for window events by inspecting the focused node in the tree.
func (rt *Runtime) EnrichWindowEvent(ev *starlark.Dict) error {
	id, ws, fs, ok, err := rt.focusedNodeInfo()
	if err != nil {
		return err
	}

	if !ok {
		_ = ev.SetKey(starlark.String("con_id"), starlark.None)
		_ = ev.SetKey(starlark.String("workspace_num"), starlark.None)
		_ = ev.SetKey(starlark.String("fullscreen_mode"), starlark.None)
		if rt.debug && rt.debugf != nil {
			rt.debugf("window enrich: focused node not found")
		}
		return nil
	}

	_ = ev.SetKey(starlark.String("con_id"), starlark.MakeInt64(id))
	_ = ev.SetKey(starlark.String("workspace_num"), starlark.MakeInt64(ws))
	_ = ev.SetKey(starlark.String("fullscreen_mode"), starlark.MakeInt64(fs))

	if rt.debug && rt.debugf != nil {
		rt.debugf("window enrich: con_id=%d workspace_num=%d fullscreen_mode=%d", id, ws, fs)
	}
	return nil
}

func (rt *Runtime) focusedNodeInfo() (conID int64, wsNum int64, fullscreenMode int64, ok bool, err error) {
	anyv, err := rt.getTreeAny()
	if err != nil {
		return 0, 0, 0, false, err
	}
	root, ok2 := anyv.(map[string]any)
	if !ok2 {
		return 0, 0, 0, false, fmt.Errorf("get_tree: unexpected root type %T", anyv)
	}

	type info struct {
		id int64
		ws int64
		fs int64
	}

	var find func(n map[string]any, curWS int64) (info, bool)
	find = func(n map[string]any, curWS int64) (info, bool) {
		if t, ok := n["type"].(string); ok && t == "workspace" {
			if v, ok := n["num"].(float64); ok {
				curWS = int64(v)
			}
		}

		if foc, ok := n["focused"].(bool); ok && foc {
			id := int64(0)
			if v, ok := n["id"].(float64); ok {
				id = int64(v)
			}
			fs := int64(0)
			if v, ok := n["fullscreen_mode"].(float64); ok {
				fs = int64(v)
			}
			return info{id: id, ws: curWS, fs: fs}, true
		}

		for _, child := range childNodes(n, "nodes") {
			if got, ok := find(child, curWS); ok {
				return got, true
			}
		}
		for _, child := range childNodes(n, "floating_nodes") {
			if got, ok := find(child, curWS); ok {
				return got, true
			}
		}
		return info{}, false
	}

	got, ok3 := find(root, 0)
	if !ok3 {
		return 0, 0, 0, false, nil
	}
	return got.id, got.ws, got.fs, true, nil
}

func childNodes(n map[string]any, key string) []map[string]any {
	v, ok := n[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (rt *Runtime) findInTree(c FindCriteria) ([]map[string]any, error) {
	anyv, err := rt.getTreeAny()
	if err != nil {
		return nil, err
	}
	root, ok := anyv.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get_tree: unexpected root type %T", anyv)
	}

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.find: fields=%v match_keys=%d limit=%d", c.Fields, len(c.Match), c.Limit)
	}

	var out []map[string]any

	var walk func(n map[string]any, curWS int64)
	walk = func(n map[string]any, curWS int64) {
		if c.Limit > 0 && len(out) >= c.Limit {
			return
		}

		if t, ok := n["type"].(string); ok && t == "workspace" {
			if v, ok := n["num"].(float64); ok {
				curWS = int64(v)
			}
		}

		if matchesNode(n, curWS, c.Match) {
			out = append(out, projectNode(n, curWS, c.Fields))
			if c.Limit > 0 && len(out) >= c.Limit {
				return
			}
		}

		for _, child := range childNodes(n, "nodes") {
			walk(child, curWS)
			if c.Limit > 0 && len(out) >= c.Limit {
				return
			}
		}
		for _, child := range childNodes(n, "floating_nodes") {
			walk(child, curWS)
			if c.Limit > 0 && len(out) >= c.Limit {
				return
			}
		}
	}

	walk(root, 0)

	if rt.debug && rt.debugf != nil {
		rt.debugf("i3.find: results=%d", len(out))
	}
	return out, nil
}

func matchesNode(n map[string]any, wsNum int64, match map[string]any) bool {
	for k, want := range match {
		key := strings.TrimSpace(strings.ToLower(k))

		switch key {
		case "workspace_num":
			w, ok := want.(int64)
			if !ok {
				return false
			}
			if wsNum != w {
				return false
			}
			continue
		case "con_id", "id":
			w, ok := want.(int64)
			if !ok {
				return false
			}
			id, ok := nodeInt64(n, "id")
			if !ok || id != w {
				return false
			}
			continue
		case "fullscreen":
			w, ok := want.(bool)
			if !ok {
				return false
			}
			fs := int64(0)
			if v, ok := nodeInt64(n, "fullscreen_mode"); ok {
				fs = v
			}
			if (fs != 0) != w {
				return false
			}
			continue
		case "fullscreen_mode":
			w, ok := want.(int64)
			if !ok {
				return false
			}
			fs, ok := nodeInt64(n, "fullscreen_mode")
			if !ok || fs != w {
				return false
			}
			continue
		case "focused":
			w, ok := want.(bool)
			if !ok {
				return false
			}
			got, ok := n["focused"].(bool)
			if !ok || got != w {
				return false
			}
			continue
		case "name":
			w, ok := want.(string)
			if !ok {
				return false
			}
			got, ok := n["name"].(string)
			if !ok || got != w {
				return false
			}
			continue
		case "app_id":
			w, ok := want.(string)
			if !ok {
				return false
			}
			got, ok := n["app_id"].(string)
			if !ok || got != w {
				return false
			}
			continue
		case "type":
			w, ok := want.(string)
			if !ok {
				return false
			}
			got, ok := n["type"].(string)
			if !ok || got != w {
				return false
			}
			continue
		default:
			// Best-effort: compare direct scalar fields.
			got, exists := n[key]
			if !exists {
				// try original key spelling too
				got, exists = n[k]
				if !exists {
					return false
				}
			}
			if !scalarEqual(got, want) {
				return false
			}
		}
	}
	return true
}

func scalarEqual(got any, want any) bool {
	switch w := want.(type) {
	case nil:
		return got == nil
	case bool:
		gb, ok := got.(bool)
		return ok && gb == w
	case string:
		gs, ok := got.(string)
		return ok && gs == w
	case int64:
		switch g := got.(type) {
		case float64:
			return int64(g) == w
		case int64:
			return g == w
		default:
			return false
		}
	default:
		return false
	}
}

func nodeInt64(n map[string]any, key string) (int64, bool) {
	v, ok := n[key]
	if !ok || v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	default:
		return 0, false
	}
}

func projectNode(n map[string]any, wsNum int64, fields []string) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		name := strings.TrimSpace(f)
		key := strings.ToLower(name)

		switch key {
		case "workspace_num":
			out[name] = wsNum
		case "con_id", "id":
			if id, ok := nodeInt64(n, "id"); ok {
				out[name] = id
			} else {
				out[name] = nil
			}
		case "fullscreen_mode":
			if fs, ok := nodeInt64(n, "fullscreen_mode"); ok {
				out[name] = fs
			} else {
				out[name] = int64(0)
			}
		case "focused":
			if v, ok := n["focused"].(bool); ok {
				out[name] = v
			} else {
				out[name] = false
			}
		case "name":
			if v, ok := n["name"].(string); ok {
				out[name] = v
			} else {
				out[name] = ""
			}
		case "app_id":
			if v, ok := n["app_id"].(string); ok {
				out[name] = v
			} else {
				out[name] = ""
			}
		case "type":
			if v, ok := n["type"].(string); ok {
				out[name] = v
			} else {
				out[name] = ""
			}
		default:
			// Best-effort: pass through scalar fields if present.
			if v, ok := n[name]; ok {
				out[name] = v
			} else if v, ok := n[key]; ok {
				out[name] = v
			} else {
				out[name] = nil
			}
		}
	}
	return out
}

type module struct {
	name  string
	attrs starlark.StringDict
	keys  []string
}

func newModule(name string, attrs starlark.StringDict) *module {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return &module{name: name, attrs: attrs, keys: keys}
}

func (m *module) String() string        { return fmt.Sprintf("<%s>", m.name) }
func (m *module) Type() string          { return "module" }
func (m *module) Freeze()               { for _, v := range m.attrs { v.Freeze() } }
func (m *module) Truth() starlark.Bool  { return starlark.True }
func (m *module) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: %s", m.Type()) }

func (m *module) Attr(name string) (starlark.Value, error) {
	v, ok := m.attrs[name]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (m *module) AttrNames() []string {
	// Return a copy to preserve immutability expectations.
	out := make([]string, len(m.keys))
	copy(out, m.keys)
	return out
}

func asString(v starlark.Value) (string, bool) {
	s, ok := v.(starlark.String)
	if ok {
		return string(s), true
	}
	if ss, ok2 := starlark.AsString(v); ok2 {
		return ss, true
	}
	return "", false
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
