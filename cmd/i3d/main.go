package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"i3d/internal/daemon"
)

func usage(out *os.File) {
	fmt.Fprintf(out, `i3d - small i3wm daemon embedding Starlark scripts

Watches:
  $HOME/.config/i3d/*.starlark
  (override with I3D_DIR=/path)

Behavior:
  - Reloads scripts on add/change/remove (debounced)
  - Dispatches i3 events to handlers declared in scripts:
      on_workspace(e), on_output(e), on_mode(e), on_window(e),
      on_barconfig_update(e), on_binding(e)

Environment:
  I3D_DIR=/path  Override scripts directory
  DEBUG=1        Enable daemon debug logs (script print(...) always prints)

Usage:
  i3d [--help|-h]

`)
}

func main() {
	var help bool
	var helpShort bool

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&helpShort, "h", false, "show help")
	fs.Usage = func() { usage(os.Stderr) }

	if err := fs.Parse(os.Args[1:]); err != nil {
		// Treat flag parsing errors as CLI usage errors.
		fmt.Fprintf(os.Stderr, "i3d: %v\n\n", err)
		usage(os.Stderr)
		os.Exit(2)
	}
	if help || helpShort {
		usage(os.Stdout)
		os.Exit(0)
	}

	debug := os.Getenv("DEBUG") == "1"

	dir := os.Getenv("I3D_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "i3d: failed to resolve home dir: %v\n", err)
			os.Exit(1)
		}
		dir = filepath.Join(home, ".config", "i3d")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := daemon.New(dir, debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "i3d: init failed: %v\n", err)
		os.Exit(1)
	}
	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "i3d: exited with error: %v\n", err)
		os.Exit(1)
	}
