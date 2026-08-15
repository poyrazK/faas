// Package wire holds process bootstrap and dependency wiring shared by every
// daemon. Keeping the boilerplate here means each cmd/<daemon>/main.go is a few
// lines and no daemon grows its own copy of signal/logging handling.
package wire

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
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

// Daemon boots a daemon: parses the standard flags (--config, --version, --help),
// builds the logger, installs SIGINT/SIGTERM cancellation, runs fn, and exits
// non-zero on error. It is the single entrypoint every cmd/<daemon>/main.go
// calls.
//
// SIGHUP is also wired (issue #518 PR-A): the handler re-reads FAAS_LOG_LEVEL
// and atomically swaps the shared slog.LevelVar. Operators reload log level
// with `kill -HUP $(pidof <daemon>)` (or systemd's `systemctl kill -s HUP
// faas-<daemon>.service`); the legacy gatewayd unit's ExecReload annotation
// is no longer relevant (Tier A7 split removed that unit — see ADR-070).
func Daemon(name string, fn RunFunc) {
	configPath := flag.String("config", "/etc/faas/"+name+".toml", "path to the daemon's TOML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	showHelp := flag.Bool("help", false, "print usage and exit")
	flag.Bool("h", false, "alias for --help")
	flag.Parse()

	if *showHelp {
		fmt.Printf("%s — onebox-faas daemon\n\n", name)
		fmt.Println("Flags:")
		flag.PrintDefaults()
		fmt.Printf("\nVersion: %s\n", Version)
		return
	}

	if *showVersion {
		fmt.Printf("%s %s\n", name, Version)
		return
	}

	// "version" only — NewCorrelationLogger stamps FieldDaemon ("daemon")
	// from the name argument below. Stamping it here too emitted the key
	// twice on every record (issue #852): duplicate JSON keys are
	// parser-dependent (jq/Loki keep the last, some decoders reject the
	// record outright). The correlation layer owns the daemon field; this
	// envelope owns version.
	log := NewCorrelationLogger(
		Logger().With("version", Version),
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

// TLSReloader is the contract pkg/wire.WatchTLSReload drives: a small
// setter that swaps the daemon's *tls.Config on a successful SIGHUP-driven
// reload. Production implementations a a struct holding the config
// behind an atomic.Pointer (or sync/atomic.Value) so gRPC's tlsCreds
// reads a stable *tls.Config pointer at handshake time and the
// WatchTLSReload goroutine swaps it without contending with the
// handshake path.
//
// Set is called only on successful reload — a failed reload keeps the
// prior config live. WatchTLSReload's contract is best-effort
// (matches vmmd's watchEgressBundleReload at cmd/vmmd/egress_bundle.go
// and pkg/wire.watchLogLevelReload): a malformed cert file does NOT
// brick the daemon's mTLS leg.
type TLSReloader interface {
	// Set replaces the daemon's live *tls.Config with cfg. Called
	// after WatchTLSReload's reload closure returns a fresh
	// *tls.Config on a successful reload. WatchTLSReload never
	// invokes Set on a nil cfg.
	Set(cfg *tls.Config)
}

// TLSRotator is the production pkg/wire.TLSReloader implementation:
// an atomic.Pointer[tls.Config] holding the live config, goroutine-
// safe Set / Get / Reload. Used by every daemon (schedd, vmmd, apid)
// that adopts the SIGHUP-driven cert rotation slice (ADR-052 §5 /
// PR-E).
//
// The rotator serves two purposes:
//
//   - WatchTLSReload calls Set on every successful SIGHUP-driven
//     reload. Handshake paths (gRPC's tlsCreds holds the original
//     *tls.Config for the listener's lifetime) consult the rotator
//     via the Reload closure installed at startup; stdlib invokes
//     that closure on every handshake so a Set between two
//     handshakes is observable to the second handshake. No listener
//     rebuild required.
//
//   - Dialer closures that captured the boot-time *tls.Config read
//     via Get() at dial time, so a SIGHUP-driven swap between dials
//     surfaces the new material on the next dial without restart.
//
// Sync: atomic.Pointer is the load-bearing primitive. Set is called
// only on successful reload (WatchTLSReload never swaps on error).
// Get / Reload may be called from any goroutine.
type TLSRotator struct {
	ptr atomic.Pointer[tls.Config]
}

// NewTLSRotator builds a rotator holding initial. A nil initial is
// tolerated and degrades to "no TLS configured" — Set becomes a
// silent no-op (the rotator never acquired material). This keeps
// the single-box / no-cluster paths from crashing on boot.
func NewTLSRotator(initial *tls.Config) *TLSRotator {
	r := &TLSRotator{}
	if initial != nil {
		r.ptr.Store(initial)
	}
	return r
}

// Set replaces the rotator's live *tls.Config. Called by
// pkg/wire.WatchTLSReload after a successful reload. A nil cfg is
// silently dropped so a buggy loader that returns (nil, nil) on
// success doesn't silently null out an active rotator —
// WatchTLSReload already warns on this case.
func (r *TLSRotator) Set(cfg *tls.Config) {
	if cfg == nil || r == nil {
		return
	}
	r.ptr.Store(cfg)
}

// Get returns the rotator's live *tls.Config, or nil if no
// material has ever been Set (the single-box / empty-cluster
// posture). Callers that branch on a nil cfg should treat the
// nil return the same as Load*TLSConfig's (nil, nil) contract:
// the dial/listen site stays in the legacy shape.
func (r *TLSRotator) Get() *tls.Config {
	if r == nil {
		return nil
	}
	return r.ptr.Load()
}

// Reload returns a wire.ReloadFunc the Load*TLSConfigWithReload
// factory can hand to stdlib. On every handshake, stdlib consults
// the closure, which reads the live *tls.Config from the rotator.
// The closure is goroutine-safe: it holds only the rotator's
// pointer (an atomic load) and never writes through it.
//
// The closure is the unit of freshness: stdlib re-runs it on
// every handshake, so a SIGHUP-driven Set between two handshakes
// is observable to the second handshake. gRPC's tlsCreds keeps the
// outer *tls.Config for the listener's lifetime; the reload
// handshake path is the per-handshake indirection that surfaces
// the rotated material.
//
// initial is the daemon-supplied fallback (the startup value, in
// case WatchTLSReload never fires): the closure returns initial
// when the rotator is empty, so a single-box daemon that loses
// its rotator still hands stdlib something usable.
func (r *TLSRotator) Reload(initial *tls.Config) func() (*tls.Config, error) {
	return func() (*tls.Config, error) {
		if r == nil {
			return initial, nil
		}
		if cur := r.ptr.Load(); cur != nil {
			return cur, nil
		}
		return initial, nil
	}
}

// WatchTLSReload is the SIGHUP-driven TLS cert rotation helper
// (ADR-052 §5 / PR-E). On every hupCh receive, it calls reload(),
// then Set()s the resulting *tls.Config on reloader. A failed
// reload is Warn-logged with the path + error; the prior config
// stays live (no Set call). The function never mutates hupCh
// from the inside — production wraps it via signal.Notify and
// tests inject their own channel.
//
// reload==nil returns immediately (no reload wired). The SIGHUP
// path still installs at the call site (daemons install the
// channel unconditionally); WatchTLSReload's early-return keeps
// the production hot path silent when the daemon is single-box
// with no TLS cluster configured.
//
// The reload closure is called OUTSIDE the watcher loop on every
// hupCh receive; the caller is responsible for keeping reload
// goroutine-safe (gRPC's tlsCreds invokes the per-handshake
// callback concurrently with the SIGHUP-driven reload). See
// tlsRotator in cmd/schedd/tls_rotator.go for the canonical
// production pattern.
func WatchTLSReload(ctx context.Context, log *slog.Logger, hupCh <-chan os.Signal, reloader TLSReloader, reload ReloadFunc) {
	if reload == nil || reloader == nil {
		// Either no reload wired (single-box with no TLS cluster)
		// or no rotator installed. Stay quiet — the SIGHUP
		// channel still drains (it's owned by the caller, who
		// may have other reloaders), but we don't consume it.
		log.Debug("wire: TLS reload disabled (nil reload or reloader)")
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hupCh:
		}
		next, err := reload()
		if err != nil {
			log.Warn("wire: TLS reload failed; keeping prior material",
				"err", logsanitize.Field(err.Error()))
			continue
		}
		if next == nil {
			log.Warn("wire: TLS reload returned nil config; keeping prior material")
			continue
		}
		reloader.Set(next)
		log.Info("wire: TLS config reloaded")
	}
}
