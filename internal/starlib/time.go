package starlib

import (
	"time"

	"go.starlark.net/starlark"
)

func (rt *Runtime) timeAttrs() starlark.StringDict {
	return starlark.StringDict{
		"now_sec": starlark.NewBuiltin("time.now_sec", rt.builtinTimeNowSec),
	}
}

func (rt *Runtime) builtinTimeNowSec(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt64(time.Now().Unix()), nil
}
