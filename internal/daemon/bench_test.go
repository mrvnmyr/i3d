package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	i3ipc "github.com/mdirkse/i3ipc-go"

	"i3d/internal/starlib"
)

func benchWriteScripts(dir string, n int) error {
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%03d-bench-%d.starlark", i, i)
		path := filepath.Join(dir, name)

		// Keep scripts minimal: no i3/exec usage so benches don't require a live i3 socket.
		src := fmt.Sprintf(`priority = %d

def on_workspace(e):
    # no-op
    return
`, i%10)

		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func newBenchRuntime() *starlib.Runtime {
	debug := os.Getenv("DEBUG") == "1"
	return starlib.NewRuntime(nil, starlib.NewExecRunner(context.Background()), debug, nil, func(string, ...any) {})
}

func BenchmarkLoadAll(b *testing.B) {
	cases := []struct {
		name    string
		scripts int
	}{
		{"scripts_10", 10},
		{"scripts_50", 50},
		{"scripts_200", 200},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			rt := newBenchRuntime()

			dir := b.TempDir()
			if err := benchWriteScripts(dir, tc.scripts); err != nil {
				b.Fatalf("write scripts: %v", err)
			}

			// Sanity check once outside the timer.
			reg0, errs0 := LoadAll(rt, dir)
			if len(errs0) != 0 {
				var sb strings.Builder
				for _, e := range errs0 {
					sb.WriteString(e.Error())
					sb.WriteByte('\n')
				}
				b.Fatalf("LoadAll sanity errors:\n%s", sb.String())
			}
			if reg0.ScriptCount() != tc.scripts {
				b.Fatalf("LoadAll sanity: scripts=%d want=%d", reg0.ScriptCount(), tc.scripts)
			}

			if os.Getenv("DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "i3d[bench] BenchmarkLoadAll %s dir=%s\n", tc.name, dir)
			}

			b.ReportAllocs()
			b.ResetTimer()

			var sink *Registry
			for i := 0; i < b.N; i++ {
				reg, errs := LoadAll(rt, dir)
				if len(errs) != 0 {
					b.Fatalf("LoadAll: errors=%d", len(errs))
				}
				sink = reg
			}
			_ = sink
		})
	}
}

func BenchmarkDispatch_Workspace(b *testing.B) {
	rt := newBenchRuntime()

	dir := b.TempDir()
	// One script with one handler to measure the core dispatch path.
	path := filepath.Join(dir, "10-dispatch-bench.starlark")
	src := `priority = 10

def on_workspace(e):
    # tiny amount of work to keep the call "real"
    if e.get("change", "") == "":
        return
    return
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		b.Fatalf("write script: %v", err)
	}

	reg, errs := LoadAll(rt, dir)
	if len(errs) != 0 {
		b.Fatalf("LoadAll: errors=%d", len(errs))
	}
	if reg.HandlerCount() != 1 {
		b.Fatalf("handlers=%d want=1", reg.HandlerCount())
	}

	debug := os.Getenv("DEBUG") == "1"
	d := &Daemon{debug: debug}
	d.reg.Store(reg)

	ev := i3ipc.Event{
		Type:   i3ipc.I3WorkspaceEvent,
		Change: "focus",
	}

	if debug {
		fmt.Fprintf(os.Stderr, "i3d[bench] BenchmarkDispatch_Workspace handlers=%d\n", reg.HandlerCount())
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		d.dispatch(rt, ev)
	}
}
