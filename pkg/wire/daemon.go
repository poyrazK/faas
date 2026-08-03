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
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Version is stamped at build time via -ldflags "-X .../pkg/wire.Version=...".
var Version = "dev"

// EnvLogLevel is the operator-facing env var that controls the slog.Level
// every daemon's JSON handler emits (issue #518 PR-A). It defaults to
// slog.LevelInfo when unset, empty, or unparseable. SIGHUP at runtime
// re-reads the value, so a `FAAS_LOG_LEVEL=debug && kill -HUP $PID` cycle
// elevates the verbosity without restarting the daemon.
//
// Stable names: debug | info | warn | error (case-insensitive). Everything
// else falls back to info with a one-shot warn log so the operator notices
// the typo.
const EnvLogLevel = "FAAS_LOG_LEVEL"

// Canonical env-string synonyms for each slog.Level. The aliases cover
// common operator spellings ("warning" for warn, "err" for error) so a
// typo in /etc/faas/sealed.env still maps to the expected level. Each
// is exported via a package-level constant for goconst; the strings are
// stable wire-contract terms, so the duplication in ParseLevel is
// enforced into named constants rather than magic strings.
const (
	levelNameDebug = "debug"
	levelNameInfo  = "info"
	levelNameWarn  = "warn"
	levelNameError = "error"

	levelAliasWarn  = "warning"
	levelAliasError = "err"
)

// logLevel is the shared, goroutine-safe leveler behind every daemon's
// JSON handler. SIGHUP mutates it in place via slog.LevelVar.Set; the
// handler re-reads it on every emit, so existing *slog.Logger values
// obtained from Logger() see the change without re-creation.
//
// slog.LevelVar is documented as safe for concurrent Set and Level calls;
// we use the stdlib implementation rather than rolling our own.
var logLevel = func() *slog.LevelVar {
	lv := &slog.LevelVar{}
	lvl, _ := ParseLevel(os.Getenv(EnvLogLevel))
	lv.Set(lvl)
	return lv
}()

// ParseLevel maps an env-supplied string to slog.Level. It returns
// (slog.LevelInfo, false) for the empty string, the literal "info", and
// any unrecognised value — the boolean indicates whether the input was
// recognized (callers can choose to log a warn on the false case). The
// function never returns an error: an unparseable value is equivalent to
// "info" by design (spec §Conventions: never refuse to start on log
// misconfiguration).
//
// The match is case-insensitive; "DEBUG", "Debug", "debug" all parse.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return slog.LevelInfo, false
	case levelNameDebug:
		return slog.LevelDebug, true
	case levelNameInfo:
		return slog.LevelInfo, true
	case levelNameWarn, levelAliasWarn:
		return slog.LevelWarn, true
	case levelNameError, levelAliasError:
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// Logger returns the standard structured JSON logger used platform-wide (spec
// §Conventions: slog JSON). Secret values must never be passed to it.
//
// The handler's Level is sourced from the shared slog.LevelVar that SIGHUP
// mutates, so operators can reconfigure verbosity at runtime without
// re-creating the logger. Pass slog.LevelInfo through HandlerOptions only
// as a fallback; the Leveler wins at emit time anyway.
//
// Issue #555 PR-3: the JSON handler is wrapped with otelinit.NewSlogBridge
// so every record emitted on a context carrying a SpanContext picks up
// trace_id / span_id attributes. The wrapper reads context from
// slog.Handler.Handle; callers that use Logger().InfoContext(ctx, ...)
// (the slog idiom for contextual emit) get the trace IDs. Plain
// Logger().Info(...) is preserved unchanged — the bridge no-ops when
// context has no span.
func Logger() *slog.Logger {
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})
	return slog.New(NewSlogBridge(h))
}

// RunFunc is a daemon's main body. It should block until ctx is cancelled and
// return nil on clean shutdown.
type RunFunc func(ctx context.Context, log *slog.Logger) error

// Daemon boots a daemon: parses the standard flags (--config, --version), builds
// the logger, installs SIGINT/SIGTERM cancellation, runs fn, and exits non-zero
// on error. It is the single entrypoint every cmd/<daemon>/main.go calls.
//
// SIGHUP is also wired (issue #518 PR-A): the handler re-reads FAAS_LOG_LEVEL
// and atomically swaps the shared slog.LevelVar. systemd's ExecReload=/bin/kill
// -HUP $MAINPID is the canonical reload gesture on the EX44; see
// deploy/systemd/faas-gatewayd.service:28 and the ansible-managed copy at
// deploy/ansible/roles/gatewayd_service/files/faas-gatewayd.service:28 for
// the precedent.
func Daemon(name string, fn RunFunc) {
	configPath := flag.String("config", "/etc/faas/"+name+".toml", "path to the daemon's TOML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", name, Version)
		return
	}

	log := NewCorrelationLogger(
		Logger().With("daemon", name, "version", Version),
		CorrelationFields{RequestID: NewRequestID()},
		name,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	defer signal.Stop(hupCh)

	go watchLogLevelReload(ctx, log, hupCh, os.Getenv)

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

// watchLogLevelReload is the SIGHUP-driven logger-level reload goroutine.
// On every hupCh receive, it re-reads FAAS_LOG_LEVEL via the supplied
// getenv (production: os.Getenv; tests: a stub that returns the desired
// value), mutates the shared slog.LevelVar in place, and emits a
// log_level_changed record with the previous and new levels. A non-empty
// but unparseable value produces a one-shot warn and falls back to info —
// the daemon never refuses to start on log misconfiguration (spec
// §Conventions).
//
// Extracted from Daemon() so the unit test can drive it directly with
// a synthetic hupCh; the production path is identical except for the
// source of the signal (kernel vs the test's chan). The function never
// mutates the hupCh from the inside — production wraps it via
// signal.Notify(hupCh, syscall.SIGHUP), and the test injects its own.
func watchLogLevelReload(ctx context.Context, log *slog.Logger, hupCh <-chan os.Signal, getenv func(string) string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hupCh:
		}
		prev := logLevel.Level()
		raw := getenv(EnvLogLevel)
		next, ok := ParseLevel(raw)
		logLevel.Set(next)
		if !ok && raw != "" {
			log.Warn("log level unrecognised; falling back to info",
				"env", EnvLogLevel, "got", logsanitize.Field(raw), "fallback", "info")
		}
		log.Info("log level changed", "prev", prev.String(), "next", next.String())
	}
}
