package daemon

import (
	"path/filepath"
	"sort"

	i3ipc "github.com/mdirkse/i3ipc-go"
	"go.starlark.net/starlark"
)

type Handler struct {
	Path      string
	Priority  int
	EventType i3ipc.EventType
	EventName string
	Callable  starlark.Callable
	Thread    *starlark.Thread
}

type Registry struct {
	ByEvent map[i3ipc.EventType][]Handler

	scriptCount  int
	handlerCount int
}

func NewRegistry() *Registry {
	return &Registry{
		ByEvent: map[i3ipc.EventType][]Handler{},
	}
}

func (r *Registry) AddScript(s *Script) {
	r.scriptCount++

	for et, fn := range s.Handlers {
		h := Handler{
			Path:      s.Path,
			Priority:  s.Priority,
			EventType: et,
			EventName: eventTypeToName(et),
			Callable:  fn,
			Thread:    s.Thread,
		}
		r.ByEvent[et] = append(r.ByEvent[et], h)
		r.handlerCount++
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

func eventTypeToName(et i3ipc.EventType) string {
	switch et {
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
