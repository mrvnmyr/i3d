package daemon

import (
	"path/filepath"
	"sort"

	i3 "go.i3wm.org/i3/v4"
	"go.starlark.net/starlark"
)

type Handler struct {
	Path      string
	Priority  int
	EventType i3.EventType
	EventName string
	Callable  starlark.Callable
	Thread    *starlark.Thread
}

// ThreadValue returns the starlark thread to run the handler on.
func (h Handler) ThreadValue() *starlark.Thread { return h.Thread }

// CallableValue returns the callable to execute for this handler.
func (h Handler) CallableValue() starlark.Callable { return h.Callable }

type ScriptInfo struct {
	Path         string
	Priority     int
	HandlerCount int
	Events       []string // event names, sorted
}

type Registry struct {
	ByEvent map[i3.EventType][]Handler
	Scripts map[string]ScriptInfo // key: absolute script path

	scriptCount  int
	handlerCount int
}

func NewRegistry() *Registry {
	return &Registry{
		ByEvent: map[i3.EventType][]Handler{},
		Scripts: map[string]ScriptInfo{},
	}
}

func (r *Registry) AddScript(s *Script) {
	r.scriptCount++

	events := make([]string, 0, len(s.Handlers))
	for et, fn := range s.Handlers {
		evName := eventTypeToName(et)
		events = append(events, evName)

		h := Handler{
			Path:      s.Path,
			Priority:  s.Priority,
			EventType: et,
			EventName: evName,
			Callable:  fn,
			Thread:    s.Thread,
		}
		r.ByEvent[et] = append(r.ByEvent[et], h)
		r.handlerCount++
	}
	sort.Strings(events)

	r.Scripts[s.Path] = ScriptInfo{
		Path:         s.Path,
		Priority:     s.Priority,
		HandlerCount: len(s.Handlers),
		Events:       events,
	}
}

func (r *Registry) Finalize() {
	for et := range r.ByEvent {
		hs := r.ByEvent[et]
		sort.SliceStable(hs, func(i, j int) bool {
			// Higher priority first.
			if hs[i].Priority != hs[j].Priority {
				return hs[i].Priority > hs[j].Priority
			}
			// Deterministic tie-breaker by filename.
			return filepath.Base(hs[i].Path) < filepath.Base(hs[j].Path)
		})
		r.ByEvent[et] = hs
	}
}

func (r *Registry) ScriptCount() int  { return r.scriptCount }
func (r *Registry) HandlerCount() int { return r.handlerCount }

func (r *Registry) ScriptPathsSorted() []string {
	out := make([]string, 0, len(r.Scripts))
	for p := range r.Scripts {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func eventTypeToName(et i3.EventType) string {
	switch et {
	case i3.WorkspaceEventType:
		return "workspace"
	case i3.OutputEventType:
		return "output"
	case i3.ModeEventType:
		return "mode"
	case i3.WindowEventType:
		return "window"
	case i3.BarconfigUpdateEventType:
		return "barconfig_update"
	case i3.BindingEventType:
		return "binding"
	default:
		return "unknown"
	}
}
