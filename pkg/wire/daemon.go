// Package wire holds process bootstrap and dependency wiring shared by every
// daemon. Keeping the boilerplate here means each cmd/<daemon>/main.go is a few
// lines and no daemon grows its own copy of signal/logging handling.
package wire

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Version is stamped at build time via -ldflags "-X .../pkg/wire.Version=...".
var Version = "dev"

// Logger returns the standard structured JSON logger used platform-wide (spec
// §Conventions: slog JSON). Secret values must never be passed to it.
func Logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// RunFunc is a daemon's main body. It should block until ctx is cancelled and
// return nil on clean shutdown.
type RunFunc func(ctx context.Context, log *slog.Logger) error

// Daemon boots a daemon: parses the standard flags (--config, --version), builds
// the logger, installs SIGINT/SIGTERM cancellation, runs fn, and exits non-zero
// on error. It is the single entrypoint every cmd/<daemon>/main.go calls.
func Daemon(name string, fn RunFunc) {
	configPath := flag.String("config", "/etc/faas/"+name+".toml", "path to the daemon's TOML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", name, Version)
		return
	}

	log := Logger().With("daemon", name, "version", Version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting", "config", *configPath)
	if err := fn(ctx, log); err != nil {
		log.Error("exited with error", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// StubRun is a placeholder body for daemons whose real logic lands in a later
// milestone. It logs readiness and blocks until shutdown so the process behaves
// like a real systemd unit during M0 wiring. Replace per milestone.
func StubRun(milestone string) RunFunc {
	return func(ctx context.Context, log *slog.Logger) error {
		log.Info("stub daemon running; real logic lands later", "milestone", milestone)
		<-ctx.Done()
		return nil
	}
}
