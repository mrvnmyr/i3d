package starlib

import "go.starlark.net/starlark"

func EventValue(eventType string, change string) *starlark.Dict {
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("type"), starlark.String(eventType))
	_ = d.SetKey(starlark.String("change"), starlark.String(change))
	return d
}
