package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	i3ipc "github.com/mdirkse/i3ipc-go"

	"i3d/internal/starlib"
)

type Daemon struct {
	dir   string
	debug bool

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

	d := &Daemon{
		dir:   dir,
		debug: debug,
	}
	d.reg.Store(NewRegistry())

	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	d.debugf("starting i3 event listener")
	i3ipc.StartEventListener()
	d.debugf("started i3 event listener")

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

	i3c, err := starlib.NewI3Client(d.debug, d.debugf)
	if err != nil {
		return fmt.Errorf("init i3 IPC client: %w", err)
	}
	defer func() { _ = i3c.Close() }()

	rt := starlib.NewRuntime(i3c, execRunner, d.debug, d.debugf, d.logf)

	// Initial load.
	d.reload(rt, nil)

	// Subscribe to all known event types up-front (cheap, avoids resubscribe logic).
	eventIn := make(chan i3ipc.Event, 128)
	subErr := d.subscribeAll(ctx, eventIn)
	if subErr != nil {
		return subErr
	}

	// Debounced reload trigger with list of changed paths.
	reloadReq := make(chan []string, 8)
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
		case paths := <-reloadReq:
			d.reload(rt, paths)
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

func (d *Daemon) watchLoop(ctx context.Context, reloadReq chan<- []string) {
	const debounce = 200 * time.Millisecond
	var (
		timer   *time.Timer
		timerCh <-chan time.Time

		changed = map[string]struct{}{}
	)

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

	snapshotChanged := func() []string {
		if len(changed) == 0 {
			return nil
		}
		out := make([]string, 0, len(changed))
		for p := range changed {
			out = append(out, p)
		}
		sort.Strings(out)
		return out
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
			changed[ev.Name] = struct{}{}
			// Any relevant change: debounce then reload.
			d.debugf("fs event: %s", ev.String())
			resetTimer()
		case <-timerCh:
			timerCh = nil

			paths := snapshotChanged()
			if len(paths) == 0 {
				continue
			}

			// Non-blocking: coalesce reload requests.
			select {
			case reloadReq <- paths:
				if d.debug {
					d.debugf("reload queued: files=%d", len(paths))
				}
				changed = map[string]struct{}{}
			default:
				// Queue full: keep changed set and retry shortly (via debounce timer).
				if d.debug {
					d.debugf("reload queue full; will retry (pending=%d)", len(changed))
				}
				resetTimer()
			}
		}
	}
}

func (d *Daemon) reload(rt *starlib.Runtime, changedPaths []string) {
	old := d.reg.Load().(*Registry)

	// Normalize + sort + dedupe the changed paths (if provided).
	var paths []string
	if len(changedPaths) != 0 {
		paths = make([]string, 0, len(changedPaths))
		paths = append(paths, changedPaths...)
		sort.Strings(paths)
		dedup := make([]string, 0, len(paths))
		for _, p := range paths {
			if len(dedup) == 0 || dedup[len(dedup)-1] != p {
				dedup = append(dedup, p)
			}
		}
		paths = dedup
		d.logf("reloading scripts: files=%d", len(paths))
		if d.debug {
			d.debugf("reload paths=%v", paths)
		}
	} else {
		d.logf("loading scripts")
	}

	d.debugf("reloading scripts...")
	reg, errs := LoadAll(rt, d.dir)
	for _, e := range errs {
		d.logf("%v", e)
	}
	d.reg.Store(reg)

	// If we weren't told which files changed, report all currently loaded scripts.
	if len(paths) == 0 {
		paths = reg.ScriptPathsSorted()
	}

	logged := map[string]struct{}{}

	logScript := func(action string, info ScriptInfo) {
		base := filepath.Base(info.Path)
		evs := ""
		if len(info.Events) != 0 {
			evs = " [" + strings.Join(info.Events, ",") + "]"
		}
		d.logf("script %s: %s prio=%d handlers=%d%s", action, base, info.Priority, info.HandlerCount, evs)
		logged[info.Path] = struct{}{}
	}

	logGone := func(action string, path string, reason string) {
		base := filepath.Base(path)
		if reason != "" {
			d.logf("script %s: %s (%s)", action, base, reason)
		} else {
			d.logf("script %s: %s", action, base)
		}
		logged[path] = struct{}{}
	}

	// Log loaded/reloaded/unloaded for the touched paths first.
	for _, p := range paths {
		newInfo, newOK := reg.Scripts[p]
		oldInfo, oldOK := old.Scripts[p]

		if newOK {
			if !oldOK {
				logScript("loaded", newInfo)
			} else {
				_ = oldInfo // reserved for future diffs
				logScript("reloaded", newInfo)
			}
			continue
		}

		// Not present in new registry.
		if !oldOK {
			// Wasn't active before either; ignore (could be transient/irrelevant).
			continue
		}

		// Distinguish delete vs load error.
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				logGone("unloaded", p, "deleted")
			} else {
				logGone("unloaded", p, fmt.Sprintf("stat error: %v", err))
			}
		} else {
			logGone("disabled", p, "load error")
		}
	}

	// Also log any scripts that disappeared but weren't in the touched set
	// (belt-and-suspenders: ensures deletes are always mentioned).
	for p := range old.Scripts {
		if _, already := logged[p]; already {
			continue
		}
		if _, still := reg.Scripts[p]; still {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				logGone("unloaded", p, "deleted")
			} else {
				logGone("unloaded", p, fmt.Sprintf("stat error: %v", err))
			}
		} else {
			logGone("disabled", p, "load error")
		}
	}

	d.logf("scripts active=%d handlers=%d errors=%d",
		reg.ScriptCount(), reg.HandlerCount(), len(errs))

	d.debugf("reloaded: %d scripts, %d handlers (%d errors)",
		reg.ScriptCount(), reg.HandlerCount(), len(errs))
}

func (d *Daemon) dispatch(rt *starlib.Runtime, ev i3ipc.Event) {
	// Reset per-dispatch caches (e.g., GET_TREE) so scripts can reuse work within this event.
	rt.BeginEvent()
	defer rt.EndEvent()

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

	// Enrich window events so most scripts don't need tree queries.
	if ev.Type == i3ipc.I3WindowEvent {
		if d.debug {
			d.debugf("enriching window event")
		}
		if err := rt.EnrichWindowEvent(evObj); err != nil {
			d.logf("enrich window event: %v", err)
		}
	}

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
