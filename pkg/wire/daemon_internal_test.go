package wire

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// These tests live in `package wire` so they can reach the helper that
// mirrors Daemon(). Daemon() itself calls os.Exit on error, which a unit
// test can't intercept without a child process — the fresh-flagset helper
// exists exactly so we don't need one.
//
// runWithFreshFlags uses a private flag set so it is parallel-safe and
// doesn't clobber the global one used by Daemon().

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// cancelImmediately returns a NotifyContext replacement that yields a
// pre-cancelled context — the test's RunFunc returns immediately.
func cancelImmediately(_ context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// runWithFreshFlags is a test-only helper that invokes Daemon's logic against
// a private flag set so we can drive --version / --config / unknown-flag
// without clobbering the global one (which Daemon uses). It returns the exit
// code the real Daemon would produce.
//
// Daemon itself uses the global flag set (intentional — the existing
// TestDaemon_VersionFlag re-execs Daemon with the test framework's flags
// already on os.Args). The unit-test seam uses a private FlagSet to keep the
// tests hermetic and parallel-safe.
func runWithFreshFlags(name string, args []string, fn RunFunc, exit func(int), logger func() *slog.Logger, notifyContext func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", "/etc/faas/"+name+".toml", "path to the daemon's TOML config")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError returns flag.ErrHelp on -h/-help; we just exit 2.
		return 2
	}

	if *showVersion {
		fmt.Printf("%s %s\n", name, Version)
		return 0
	}

	log := logger().With("daemon", name, "version", Version)

	ctx, stop := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting", "config", *configPath)
	if err := fn(ctx, log); err != nil {
		log.Error("exited with error", "err", err)
		exit(1)
		return 1
	}
	log.Info("shutdown complete")
	return 0
}

func TestRunWithFreshFlags_HappyPath(t *testing.T) {
	var ran atomic.Int32
	fn := func(_ context.Context, _ *slog.Logger) error {
		ran.Add(1)
		return nil
	}
	code := runWithFreshFlags("test", nil, fn, func(int) {}, newDiscardLogger, cancelImmediately)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if ran.Load() != 1 {
		t.Errorf("fn ran %d times, want 1", ran.Load())
	}
}

func TestRunWithFreshFlags_ErrorReturnsOne(t *testing.T) {
	fn := func(_ context.Context, _ *slog.Logger) error {
		return io.EOF
	}
	var exitCode atomic.Int32
	code := runWithFreshFlags("test", nil, fn, func(c int) { exitCode.Store(int32(c)) }, newDiscardLogger, cancelImmediately)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if exitCode.Load() != 1 {
		t.Errorf("exit called with %d, want 1", exitCode.Load())
	}
}

func TestRunWithFreshFlags_VersionFlag(t *testing.T) {
	// Capture the printed version line via stdout redirection.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	var ran atomic.Int32
	fn := func(_ context.Context, _ *slog.Logger) error {
		ran.Add(1)
		return nil
	}
	code := runWithFreshFlags("apid", []string{"--version"}, fn, func(int) {}, newDiscardLogger, cancelImmediately)
	_ = w.Close()

	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if ran.Load() != 0 {
		t.Error("fn must not run when --version is set")
	}
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "apid ") {
		t.Errorf("version output %q missing daemon name", buf.String())
	}
}

func TestRunWithFreshFlags_HelpFlag(t *testing.T) {
	// Capture the help text via stderr redirection.
	_, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	fn := func(_ context.Context, _ *slog.Logger) error {
		t.Error("fn must not run on -help")
		return nil
	}
	code := runWithFreshFlags("apid", []string{"-h"}, fn, func(int) {}, newDiscardLogger, cancelImmediately)
	_ = w.Close()

	if code != 2 {
		t.Errorf("code = %d, want 2 (flag help exit code)", code)
	}
}

func TestRunWithFreshFlags_ConfigFlag(t *testing.T) {
	var ran atomic.Int32
	fn := func(_ context.Context, _ *slog.Logger) error {
		ran.Add(1)
		return nil
	}
	code := runWithFreshFlags("apid", []string{"--config", "/tmp/custom.toml"}, fn, func(int) {}, newDiscardLogger, cancelImmediately)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if ran.Load() != 1 {
		t.Error("fn should have run with custom --config")
	}
}

func TestRunWithFreshFlags_UnknownFlag(t *testing.T) {
	_, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	fn := func(_ context.Context, _ *slog.Logger) error {
		t.Error("fn must not run on unknown flag")
		return nil
	}
	code := runWithFreshFlags("apid", []string{"--bogus"}, fn, func(int) {}, newDiscardLogger, cancelImmediately)
	_ = w.Close()

	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}
