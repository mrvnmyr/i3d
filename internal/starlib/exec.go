package starlib

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"go.starlark.net/starlark"
)

type ExecRunner struct {
	ctx context.Context
}

func NewExecRunner(ctx context.Context) *ExecRunner {
	return &ExecRunner{ctx: ctx}
}

func (rt *Runtime) builtinExec(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var argvVal starlark.Value
	check := true
	capOut := true
	capErr := true

	if err := starlark.UnpackArgs(
		b.Name(), args, kwargs,
		"args", &argvVal,
		"check?", &check,
		"capture_stdout?", &capOut,
		"capture_stderr?", &capErr,
	); err != nil {
		return nil, err
	}

	argv, err := toStringSlice(argvVal)
	if err != nil {
		return nil, fmt.Errorf("args: %w", err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("args must be non-empty")
	}

	rc, out, errOut, runErr := rt.exec.Run(argv, capOut, capErr)
	if runErr != nil {
		return nil, runErr
	}

	if check && rc != 0 {
		return nil, fmt.Errorf("exec failed (rc=%d): %s", rc, argv[0])
	}

	res := starlark.NewDict(3)
	_ = res.SetKey(starlark.String("rc"), starlark.MakeInt(rc))
	if capOut {
		_ = res.SetKey(starlark.String("stdout"), starlark.String(out))
	} else {
		_ = res.SetKey(starlark.String("stdout"), starlark.None)
	}
	if capErr {
		_ = res.SetKey(starlark.String("stderr"), starlark.String(errOut))
	} else {
		_ = res.SetKey(starlark.String("stderr"), starlark.None)
	}
	return res, nil
}

func (e *ExecRunner) Run(argv []string, captureStdout, captureStderr bool) (rc int, stdout string, stderr string, err error) {
	cmd := exec.CommandContext(e.ctx, argv[0], argv[1:]...)

	var outBuf, errBuf bytes.Buffer

	if captureStdout {
		cmd.Stdout = &outBuf
	} else {
		cmd.Stdout = os.Stdout
	}
	if captureStderr {
		cmd.Stderr = &errBuf
	} else {
		cmd.Stderr = os.Stderr
	}

	runErr := cmd.Run()
	if runErr == nil {
		return 0, outBuf.String(), errBuf.String(), nil
	}

	var exitErr *exec.ExitError
	if ok := errorAs(runErr, &exitErr); ok {
		// Non-zero exit code.
		code := exitErr.ExitCode()
		return code, outBuf.String(), errBuf.String(), nil
	}

	// Execution failure (e.g. binary not found, context canceled).
	return -1, outBuf.String(), errBuf.String(), runErr
}

// Small local helper to avoid importing errors everywhere.
func errorAs(err error, target interface{}) bool {
	type aser interface{ As(any) bool }
	if e, ok := err.(aser); ok {
		return e.As(target)
	}
	// Fallback: no As; can't inspect.
	return false
}

func toStringSlice(v starlark.Value) ([]string, error) {
	iterable, ok := v.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("expected iterable (list/tuple), got %s", v.Type())
	}
	it := iterable.Iterate()
	defer it.Done()

	var out []string
	var item starlark.Value
	for it.Next(&item) {
		s, ok := starlark.AsString(item)
		if !ok {
			return nil, fmt.Errorf("expected string, got %s", item.Type())
		}
		out = append(out, s)
	}
	return out, nil
}
