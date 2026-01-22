package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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
  I3D_HANDLER_MAX_STEPS   Max Starlark steps per handler (0 disables, default 5000000)
  I3D_HANDLER_TIMEOUT_MS  Max handler wall time in ms (0 disables, default 2000)

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

	handlerMaxSteps, err := envUint("I3D_HANDLER_MAX_STEPS", 5_000_000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "i3d: %v\n", err)
		os.Exit(2)
	}
	handlerTimeout, err := envDurationMs("I3D_HANDLER_TIMEOUT_MS", 2000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "i3d: %v\n", err)
		os.Exit(2)
	}

	d, err := daemon.New(dir, debug, handlerMaxSteps, handlerTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "i3d: init failed: %v\n", err)
		os.Exit(1)
	}
	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "i3d: exited with error: %v\n", err)
		os.Exit(1)
	}
}

func envUint(name string, def uint64) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned integer, got %q", name, raw)
	}
	return v, nil
}

func envDurationMs(name string, defMs int64) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		if defMs <= 0 {
			return 0, nil
		}
		return time.Duration(defMs) * time.Millisecond, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer (ms), got %q", name, raw)
	}
	if v <= 0 {
		return 0, nil
	}
	return time.Duration(v) * time.Millisecond, nil
}
