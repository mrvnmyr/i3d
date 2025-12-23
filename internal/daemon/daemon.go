package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	i3ipc "github.com/mdirkse/i3ipc-go"

	"i3d/internal/starlib"
)

type Daemon struct {
	dir   string
	debug bool

	i3sock *i3ipc.IPCSocket

	reg atomic.Value // *Registry

	watcher *fsnotify.Watcher
}

func New(dir string, debug bool) (*Daemon, error) {
	if dir == "" {
		return nil, fmt.Errorf("dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	sock, err := i3ipc.GetIPCSocket()
	if err != nil {
		return nil, fmt.Errorf("get i3 IPC socket: %w", err)
	}

	d := &Daemon{
		dir:    dir,
		debug:  debug,
		i3sock: sock,
	}
	d.reg.Store(NewRegistry())

	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := i3ipc.StartEventListener(); err != nil {
		return fmt.Errorf("start event listener: %w", err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify watcher: %w", err)
	}
	d.watcher = w
	defer func() { _ = w.Close() }()

	if err := w.Add(d.dir); err != nil {
		return fmt.Errorf("watch %s: %w", d.dir, err)
	}

	execRunner := starlib.NewExecRunner(ctx)
	rt := starlib.NewRuntime(d.i3sock, execRunner, d.debug, d.debugf, d.logf)

	// Initial load.
	d.reload(rt)

	// Subscribe to all known event types up-front (cheap, avoids resubscribe logic).
	eventIn := make(chan i3ipc.Event, 128)
	subErr := d.subscribeAll(ctx, eventIn)
	if subErr != nil {
		return subErr
	}

	// Debounced reload trigger.
	reloadReq := make(chan struct{}, 1)
	go d.watchLoop(ctx, reloadReq)

	d.debugf("running; script dir=%s", d.dir)

	for {
		select {
		case <-ctx.Done():
			d.debugf("context done; exiting")
			return nil
		case ev, ok := <-eventIn:
			if !ok {
				d.logf("event channel closed; exiting")
				return nil
			}
			d.dispatch(rt, ev)
		case <-reloadReq:
			d.reload(rt)
		}
	}
}

func (d *Daemon) subscribeAll(ctx context.Context, out chan<- i3ipc.Event) error {
	type sub struct {
		name string
		typ  i3ipc.EventType
	}
	subs := []sub{
		{"workspace", i3ipc.I3WorkspaceEvent},
		{"output", i3ipc.I3OutputEvent},
		{"mode", i3ipc.I3ModeEvent},
		{"window", i3ipc.I3WindowEvent},
		{"barconfig_update", i3ipc.I3BarConfigUpdateEvent},
		{"binding", i3ipc.I3BindingEvent},
	}

	for _, s := range subs {
		ch, err := i3ipc.Subscribe(s.typ)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", s.name, err)
		}
		go func(c <-chan i3ipc.Event) {
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- ev:
					default:
						// Drop if overloaded; handlers should be fast.
						// (Keeps memory footprint bounded.)
						if d.debug {
							d.debugf("dropped event=%s change=%s (queue full)", eventTypeToName(ev.Type), ev.Change)
						}
					}
				}
			}
		}(ch)
	}

	return nil
}

func (d *Daemon) watchLoop(ctx context.Context, reloadReq chan<- struct{}) {
	const debounce = 200 * time.Millisecond
	var (
		timer   *time.Timer
		timerCh <-chan time.Time
	)

	trigger := func() {
		// Non-blocking: coalesce reload requests.
		select {
		case reloadReq <- struct{}{}:
		default:
		}
	}

	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(debounce)
			timerCh = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounce)
		timerCh = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			d.logf("watch error: %v", err)
		case ev, ok := <-d.watcher.Events:
			if !ok {
				return
			}
			if !isStarlark(ev.Name) {
				continue
			}
			// Any relevant change: debounce then reload.
			d.debugf("fs event: %s", ev.String())
			resetTimer()
		case <-timerCh:
			timerCh = nil
			trigger()
		}
	}
}

func (d *Daemon) reload(rt *starlib.Runtime) {
	d.debugf("reloading scripts...")
	reg, errs := LoadAll(rt, d.dir)
	for _, e := range errs {
		d.logf("%v", e)
	}
	d.reg.Store(reg)

	d.debugf("reloaded: %d scripts, %d handlers (%d errors)",
		reg.ScriptCount(), reg.HandlerCount(), len(errs))
}

func (d *Daemon) dispatch(rt *starlib.Runtime, ev i3ipc.Event) {
	reg := d.reg.Load().(*Registry)
	handlers := reg.ByEvent[ev.Type]
	if len(handlers) == 0 {
		if d.debug {
			d.debugf("event=%s change=%s (no handlers)", eventTypeToName(ev.Type), ev.Change)
		}
		return
	}

	if d.debug {
		d.debugf("event=%s change=%s handlers=%d", eventTypeToName(ev.Type), ev.Change, len(handlers))
	}

	evObj := starlib.EventValue(ev)
	// Freeze once; safe to share across handlers.
	evObj.Freeze()

	for _, h := range handlers {
		if d.debug {
			d.debugf(" -> %s prio=%d", filepath.Base(h.Path), h.Priority)
		}
		if err := rt.CallHandler(h, evObj); err != nil {
			// Keep daemon running even if a handler fails.
			d.logf("handler %s (%s): %v", filepath.Base(h.Path), h.EventName, err)
		}
	}
}

func (d *Daemon) debugf(format string, args ...any) {
	if !d.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "i3d[debug] "+format+"\n", args...)
}

func (d *Daemon) logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "i3d "+format+"\n", args...)
}

func isStarlark(path string) bool {
	return filepath.Ext(path) == ".starlark"
}

var errNoHandlers = errors.New("no handlers")
