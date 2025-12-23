package starlib

import (
	"fmt"
	"math"
	"sort"

	"go.starlark.net/starlark"
)

// JSONToStarlark converts decoded encoding/json values into Starlark values.
//
// Note: While encoding/json decodes numbers as float64 by default, some of our
// internal helpers (e.g. i3.find) construct Go-native int64 values. We accept
// those here too to keep the conversion generic.
func JSONToStarlark(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil

	// Native ints (produced by internal helpers, not by encoding/json).
	case int:
		return starlark.MakeInt64(int64(x)), nil
	case int8:
		return starlark.MakeInt64(int64(x)), nil
	case int16:
		return starlark.MakeInt64(int64(x)), nil
	case int32:
		return starlark.MakeInt64(int64(x)), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case uint:
		const maxI64 = uint64(^uint64(0) >> 1)
		if uint64(x) > maxI64 {
			return nil, fmt.Errorf("int out of range: %d", x)
		}
		return starlark.MakeInt64(int64(x)), nil
	case uint8:
		return starlark.MakeInt64(int64(x)), nil
	case uint16:
		return starlark.MakeInt64(int64(x)), nil
	case uint32:
		return starlark.MakeInt64(int64(x)), nil
	case uint64:
		const maxI64 = uint64(^uint64(0) >> 1)
		if x > maxI64 {
			return nil, fmt.Errorf("int out of range: %d", x)
		}
		return starlark.MakeInt64(int64(x)), nil

	case float64:
		// JSON numbers decode as float64; prefer int if it is integral and fits.
		if !math.IsInf(x, 0) && !math.IsNaN(x) && math.Trunc(x) == x {
			if x >= math.MinInt64 && x <= math.MaxInt64 {
				return starlark.MakeInt64(int64(x)), nil
			}
		}
		return starlark.Float(x), nil
	case []any:
		out := make([]starlark.Value, 0, len(x))
		for _, it := range x {
			sv, err := JSONToStarlark(it)
			if err != nil {
				return nil, err
			}
			out = append(out, sv)
		}
		return starlark.NewList(out), nil
	case map[string]any:
		d := starlark.NewDict(len(x))
		// Sort keys for deterministic output/behavior.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sv, err := JSONToStarlark(x[k])
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, fmt.Errorf("dict set %q: %w", k, err)
			}
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported JSON type: %T", v)
	}
}
