package starlib

import (
	"go.starlark.net/starlark"

	"i3d/internal/daemon"
)

// CallHandler executes a daemon.Handler.
func (rt *Runtime) CallHandler(h daemon.Handler, event starlark.Value) error {
	return rt.CallHandler2(h.Thread, h.Callable, event)
}
