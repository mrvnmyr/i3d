package starlib

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
)

type Runtime struct {
	i3    *i3ipc.IPCSocket
	exec  *ExecRunner
	debug bool

	debugf func(string, ...any)
	logf   func(string, ...any)

	i3mod starlark.Value
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

func (rt *Runtime) CallHandler(h interface {
	Thread() *starlark.Thread
	Callable() starlark.Callable
}, event starlark.Value) error {
	// This helper is kept generic for minimal coupling, but we call it from daemon
	// with the concrete Handler type via the method below.
	return fmt.Errorf("invalid handler adapter")
}

// CallHandler executes a daemon.Handler.
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
