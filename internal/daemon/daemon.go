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
	i3 "go.i3wm.org/i3/v4"

	"i3d/internal/starlib"
)

type Daemon struct {
	dir   string
	debug bool

	reg atomic.Value // *Registry

	watcher *fsnotify.Watcher

	handlerMaxSteps uint64
	handlerTimeout  time.Duration
}

type daemonEvent struct {
	Type   i3.EventType
	Change string
	Window *i3.WindowEvent
}

func New(dir string, debug bool, handlerMaxSteps uint64, handlerTimeout time.Duration) (*Daemon, error) {
	if dir == "" {
		return nil, fmt.Errorf("dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	d := &Daemon{
		dir:             dir,
		debug:           debug,
		handlerMaxSteps: handlerMaxSteps,
		handlerTimeout:  handlerTimeout,
	}
	d.reg.Store(NewRegistry())

	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
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

	// Establish the request/reply connection first. Besides making startup under
	// systemd reliable, this gives the event receiver a verified socket path.
	i3c, err := d.initI3Client(ctx)
	if err != nil {
		return fmt.Errorf("init i3 IPC client: %w", err)
	}
	defer func() { _ = i3c.Close() }()

	// Make both child processes and go-i3 use the socket we already verified.
	if sp := i3c.SocketPath(); sp != "" {
		_ = os.Setenv("I3SOCK", sp)
		i3.SocketPathHook = func() (string, error) { return sp, nil }
		if d.debug {
			d.debugf("configured i3 socket=%s", sp)
		}
	}

	rt := starlib.NewRuntime(i3c, execRunner, d.debug, d.debugf, d.logf, d.handlerMaxSteps, d.handlerTimeout)

	// Initial load.
	d.reload(rt, nil)

	// A single receiver preserves ordering and avoids one socket/goroutine per
	// event type. go-i3 reconnects with backoff after transient IPC failures.
	eventIn := make(chan daemonEvent, 128)
	eventErr := make(chan error, 1)
	receiver := i3.Subscribe(
		i3.WorkspaceEventType,
		i3.OutputEventType,
		i3.ModeEventType,
		i3.WindowEventType,
		i3.BarconfigUpdateEventType,
		i3.BindingEventType,
	)
	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		d.receiveEvents(ctx, receiver, eventIn, eventErr)
	}()
	defer func() {
		_ = receiver.Close()
		<-eventDone
	}()

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
		case err := <-eventErr:
			return fmt.Errorf("i3 event receiver: %w", err)
		case paths := <-reloadReq:
			d.reload(rt, paths)
		}
	}
}

func (d *Daemon) initI3Client(ctx context.Context) (*starlib.I3Client, error) {
	const (
		maxAttempts = 15
		interval    = 1 * time.Second
	)

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		i3c, err := starlib.NewI3Client(d.debug, d.debugf)
		if err == nil {
			if d.debug {
				d.debugf("i3 IPC init ok (attempt %d/%d)", attempt, maxAttempts)
			}
			return i3c, nil
		}
		lastErr = err

		if d.debug {
			d.debugf("i3 IPC init attempt %d/%d failed: %v", attempt, maxAttempts, err)
		}

		if attempt == maxAttempts {
			break
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unknown i3 IPC init failure")
	}
	return nil, lastErr
}

func (d *Daemon) receiveEvents(ctx context.Context, receiver *i3.EventReceiver, out chan<- daemonEvent, errOut chan<- error) {
	for receiver.Next() {
		ev, ok := adaptEvent(receiver.Event())
		if !ok {
			continue
		}

		select {
		case out <- ev:
		default:
			// Keep memory bounded while continuing to drain the IPC socket.
			if d.debug {
				d.debugf("dropped event=%s change=%s (queue full)", eventTypeToName(ev.Type), ev.Change)
			}
		}
	}

	err := receiver.Close()
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		err = errors.New("event stream ended")
	}
	select {
	case errOut <- err:
	case <-ctx.Done():
	}
}

func adaptEvent(ev i3.Event) (daemonEvent, bool) {
	switch ev := ev.(type) {
	case *i3.WorkspaceEvent:
		return daemonEvent{Type: i3.WorkspaceEventType, Change: ev.Change}, true
	case *i3.OutputEvent:
		return daemonEvent{Type: i3.OutputEventType, Change: ev.Change}, true
	case *i3.ModeEvent:
		return daemonEvent{Type: i3.ModeEventType, Change: ev.Change}, true
	case *i3.WindowEvent:
		return daemonEvent{Type: i3.WindowEventType, Change: ev.Change, Window: ev}, true
	case *i3.BarconfigUpdateEvent:
		return daemonEvent{Type: i3.BarconfigUpdateEventType}, true
	case *i3.BindingEvent:
		return daemonEvent{Type: i3.BindingEventType, Change: ev.Change}, true
	default:
		return daemonEvent{}, false
	}
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

func (d *Daemon) dispatch(rt *starlib.Runtime, ev daemonEvent) {
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

	evObj := starlib.EventValue(eventTypeToName(ev.Type), ev.Change)

	// Enrich window events so most scripts don't need tree queries.
	if ev.Type == i3.WindowEventType {
		if d.debug {
			d.debugf("enriching window event")
		}
		if ev.Window == nil {
			d.logf("enrich window event: missing window payload")
		} else if err := rt.EnrichWindowEvent(evObj, int64(ev.Window.Container.ID), int64(ev.Window.Container.FullscreenMode)); err != nil {
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
