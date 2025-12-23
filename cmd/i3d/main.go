package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"i3d/internal/daemon"
)

func main() {
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
}
