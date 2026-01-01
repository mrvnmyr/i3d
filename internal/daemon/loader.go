package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"i3d/internal/starlib"
)

type ScriptError struct {
	Path string
	Err  error
}

func (e ScriptError) Error() string {
	return fmt.Sprintf("script %s: %v", filepath.Base(e.Path), e.Err)
}

type eventDef struct {
	name string
	typ  i3ipc.EventType
}

var knownEvents = []eventDef{
	{"workspace", i3ipc.I3WorkspaceEvent},
	{"output", i3ipc.I3OutputEvent},
	{"mode", i3ipc.I3ModeEvent},
	{"window", i3ipc.I3WindowEvent},
	{"barconfig_update", i3ipc.I3BarConfigUpdateEvent},
	{"binding", i3ipc.I3BindingEvent},
}

// LoadAll loads all *.starlark files from dir and returns a fresh handler registry.
// Files with errors are skipped (errors are returned).
func LoadAll(rt *starlib.Runtime, dir string) (*Registry, []error) {
	pattern := filepath.Join(dir, "*.starlark")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return NewRegistry(), []error{fmt.Errorf("glob: %w", err)}
	}
	sort.Strings(files)

	reg := NewRegistry()
	var errs []error

	for _, path := range files {
		s, err := loadOne(rt, path)
		if err != nil {
			errs = append(errs, ScriptError{Path: path, Err: err})
			continue
		}
		reg.AddScript(s)
	}

	reg.Finalize()
	return reg, errs
}

type Script struct {
	Path     string
	Priority int
	Thread   *starlark.Thread
	Globals  starlark.StringDict
	Handlers map[i3ipc.EventType]starlark.Callable
}

func loadOne(rt *starlib.Runtime, path string) (*Script, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	thread := rt.NewThread(path)

	predeclared := rt.Predeclared(path)
	_, prog, err := starlark.SourceProgramOptions(syntax.LegacyFileOptions(), path, src, predeclared.Has)
	if err != nil {
		return nil, err
	}
	globals, err := prog.Init(thread, predeclared)
	if err != nil {
		return nil, err
	}

	priority := 0
	if v, ok := globals["priority"]; ok {
		if v == starlark.None {
			// keep default
		} else if iv, ok2 := v.(starlark.Int); ok2 {
			i64, ok3 := iv.Int64()
			if !ok3 {
				return nil, fmt.Errorf("priority out of range")
			}
			maxInt := int64(int(^uint(0) >> 1))
			minInt := -maxInt - 1
			if i64 < minInt || i64 > maxInt {
				return nil, fmt.Errorf("priority out of range")
			}
			priority = int(i64)
		} else {
			return nil, fmt.Errorf("priority must be int, got %s", v.Type())
		}
	}

	handlers := map[i3ipc.EventType]starlark.Callable{}

	// Convention: on_<eventname>(e)
	for _, ev := range knownEvents {
		name := "on_" + ev.name
		if v, ok := globals[name]; ok {
			if c, ok2 := v.(starlark.Callable); ok2 {
				handlers[ev.typ] = c
			} else {
				return nil, fmt.Errorf("%s must be callable, got %s", name, v.Type())
			}
		}
	}

	// Optional: handlers = {"workspace": fn, ...}
	if hv, ok := globals["handlers"]; ok {
		m, ok2 := hv.(starlark.IterableMapping)
		if !ok2 {
			return nil, fmt.Errorf("handlers must be a dict-like mapping, got %s", hv.Type())
		}
		iter := m.Iterate()
		defer iter.Done()
		var key starlark.Value
		for iter.Next(&key) {
			val, found, err := m.Get(key)
			if err != nil {
				return nil, fmt.Errorf("handlers[%s]: %w", key.String(), err)
			}
			if !found {
				continue
			}
			ks, ok3 := starlark.AsString(key)
			if !ok3 {
				return nil, fmt.Errorf("handlers keys must be strings, got %s", key.Type())
			}
			c, ok4 := val.(starlark.Callable)
			if !ok4 {
				return nil, fmt.Errorf("handlers[%s] must be callable, got %s", ks, val.Type())
			}
			et, ok5 := eventNameToType(strings.TrimSpace(ks))
			if !ok5 {
				return nil, fmt.Errorf("unknown event name in handlers: %q", ks)
			}
			handlers[et] = c
		}
	}

	// Optional: init()
	if iv, ok := globals["init"]; ok {
		if c, ok2 := iv.(starlark.Callable); ok2 {
			if _, err := starlark.Call(thread, c, nil, nil); err != nil {
				return nil, fmt.Errorf("init(): %w", err)
			}
		} else {
			return nil, fmt.Errorf("init must be callable, got %s", iv.Type())
		}
	}

	return &Script{
		Path:     path,
		Priority: priority,
		Thread:   thread,
		Globals:  globals,
		Handlers: handlers,
	}, nil
}

func eventNameToType(name string) (i3ipc.EventType, bool) {
	switch strings.ToLower(name) {
	case "workspace":
		return i3ipc.I3WorkspaceEvent, true
	case "output":
		return i3ipc.I3OutputEvent, true
	case "mode":
		return i3ipc.I3ModeEvent, true
	case "window":
		return i3ipc.I3WindowEvent, true
	case "barconfig_update", "bar_config_update":
		return i3ipc.I3BarConfigUpdateEvent, true
	case "binding":
		return i3ipc.I3BindingEvent, true
	default:
		return 0, false
	}
}
