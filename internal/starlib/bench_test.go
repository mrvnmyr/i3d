package starlib

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
)

var benchOnce sync.Once

func benchDebugf(format string, args ...any) {
	if os.Getenv("DEBUG") != "1" {
		return
	}
	benchOnce.Do(func() {
		// Print only once per test binary to avoid spamming benchmark output.
		fmt.Fprintf(os.Stderr, "i3d[bench] "+format+"\n", args...)
	})
}

func makeSyntheticTree(workspaces, consPerWS int) map[string]any {
	root := map[string]any{
		"type":           "root",
		"focused":        false,
		"nodes":          []any{},
		"floating_nodes": []any{},
	}

	nodes := make([]any, 0, workspaces)
	for ws := 1; ws <= workspaces; ws++ {
		wsNode := map[string]any{
			"type":           "workspace",
			"num":            float64(ws),
			"focused":        false,
			"nodes":          []any{},
			"floating_nodes": []any{},
		}

		cs := make([]any, 0, consPerWS)
		for i := 0; i < consPerWS; i++ {
			id := float64(ws*100000 + i + 1)
			con := map[string]any{
				"type":           "con",
				"id":             id,
				"fullscreen_mode": float64(0),
				"focused":        false,
				"name":           fmt.Sprintf("con-%d-%d", ws, i),
				"app_id":         "bench.app",
				"nodes":          []any{},
				"floating_nodes": []any{},
			}
			cs = append(cs, con)
		}

		// Mark one node as focused so focusedNodeInfo has work to do.
		if ws == 1 && len(cs) > 0 {
			c0 := cs[0].(map[string]any)
			c0["focused"] = true
			c0["fullscreen_mode"] = float64(1)
		}

		wsNode["nodes"] = cs
		nodes = append(nodes, wsNode)
	}

	root["nodes"] = nodes
	return root
}

func newBenchRuntimeWithTree(anyTree any) *Runtime {
	rt := NewRuntime(nil, NewExecRunner(context.Background()), os.Getenv("DEBUG") == "1", nil, func(string, ...any) {})
	// Prime the per-dispatch cache so none of these benches require a live i3 socket.
	rt.eventTree = &treeCache{any: anyTree}
	return rt
}

func BenchmarkJSONToStarlark_Tree(b *testing.B) {
	tree := makeSyntheticTree(10, 50)
	benchDebugf("BenchmarkJSONToStarlark_Tree: tree workspaces=%d cons_per_ws=%d", 10, 50)

	b.ReportAllocs()
	b.ResetTimer()

	var sink any
	for i := 0; i < b.N; i++ {
		v, err := JSONToStarlark(tree)
		if err != nil {
			b.Fatalf("JSONToStarlark: %v", err)
		}
		sink = v
	}
	_ = sink
}

func BenchmarkFocusedNodeInfo(b *testing.B) {
	tree := makeSyntheticTree(10, 50)
	rt := newBenchRuntimeWithTree(tree)
	benchDebugf("BenchmarkFocusedNodeInfo: tree workspaces=%d cons_per_ws=%d", 10, 50)

	b.ReportAllocs()
	b.ResetTimer()

	var sink int64
	for i := 0; i < b.N; i++ {
		id, ws, fs, ok, err := rt.focusedNodeInfo()
		if err != nil {
			b.Fatalf("focusedNodeInfo: %v", err)
		}
		if !ok {
			b.Fatalf("focusedNodeInfo: not found")
		}
		// Accumulate to prevent overly aggressive optimization.
		sink += id + ws + fs
	}
	_ = sink
}

func BenchmarkFindInTree_WorkspaceConIDs(b *testing.B) {
	tree := makeSyntheticTree(10, 200)
	rt := newBenchRuntimeWithTree(tree)
	benchDebugf("BenchmarkFindInTree_WorkspaceConIDs: tree workspaces=%d cons_per_ws=%d", 10, 200)

	crit := FindCriteria{
		Match: map[string]any{
			"type":          "con",
			"workspace_num": int64(1),
		},
		Fields: []string{"con_id", "workspace_num"},
		Limit:  0,
	}

	b.ReportAllocs()
	b.ResetTimer()

	var sink int
	for i := 0; i < b.N; i++ {
		res, err := rt.findInTree(crit)
		if err != nil {
			b.Fatalf("findInTree: %v", err)
		}
		sink += len(res)
	}
	_ = sink
}
