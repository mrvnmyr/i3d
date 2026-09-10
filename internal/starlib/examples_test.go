package starlib

import (
	"context"
	"path/filepath"
	"testing"

	"go.starlark.net/starlark"
)

func TestHideWindowsExampleEscapesRegexLiteral(t *testing.T) {
	// Load the repository example itself so its helper code cannot regress
	// independently from this execution test.
	path := filepath.Join("..", "..", "examples", "70-hide-windows-by-title.starlark")
	rt := NewRuntime(
		nil,
		NewExecRunner(context.Background()),
		false,
		nil,
		func(string, ...any) {},
		0,
		0,
	)
	thread := rt.NewThread(path)
	globals, err := starlark.ExecFile(thread, path, nil, rt.Predeclared(path))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}

	escape, ok := globals["_escape_regex_literal"].(starlark.Callable)
	if !ok {
		t.Fatal("_escape_regex_literal is not callable")
	}
	got, err := starlark.Call(
		thread,
		escape,
		starlark.Tuple{starlark.String(`Rust.Desk [remote] \ host`)},
		nil,
	)
	if err != nil {
		t.Fatalf("escape literal: %v", err)
	}
	gotString, ok := starlark.AsString(got)
	if !ok {
		t.Fatalf("escaped literal has type %s, want string", got.Type())
	}
	if want := `Rust\.Desk \[remote\] \\ host`; gotString != want {
		t.Fatalf("escaped literal=%q want=%q", gotString, want)
	}
}
