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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// Version is stamped at build time via -ldflags "-X .../pkg/wire.Version=...".
var Version = "dev"

// GitSHA is the abbreviated commit SHA the binary was built from.
// Stamped at build time via -ldflags "-X .../pkg/wire.GitSHA=...";
// default "unknown" for dev builds. Used by the daemon_build_info
// metric (issue #586 / ADR-129) so the operator can answer "what
// version is running on this box" without ssh'ing in.
var GitSHA = "unknown"

// BuildTime is the RFC3339 timestamp the binary was built at.
// Stamped at build time via -ldflags "-X .../pkg/wire.BuildTime=...";
// default empty for dev builds. The daemon_build_info metric
// surfaces it as a label so fleet-wide panels can show when
// different daemons were last rebuilt.
var BuildTime = ""

// defaultOps is the package-level OpsMetrics registered by the
// current daemon process via RegisterDefaultOps. Daemon() reads
// it to call RecordDaemonRestart without each daemon having to
// pass its *OpsMetrics through the RunFunc signature (issue #573
// / ADR-128). Nil-safe on read — Daemon() skips the call when
// no daemon has registered yet (cmd/<daemon>/main.go calls
// RegisterDefaultOps right after wire.NewOpsMetrics). The pointer
// is process-local; concurrent reads from Daemon() are safe
// because registration is a one-time write at startup, before
// any goroutine other than the boot path is running.
var defaultOps *OpsMetrics

// RegisterDefaultOps stores ops as the package-level default so
// RunAndShutdown / recordUptime / wire.Daemon's shutdown-flip can
// read from it. Call this once at the top of
// cmd/<daemon>/main.go, immediately after
// wire.NewOpsMetrics(name) and wire.BootStamps(ctx, name, ops).
// Passing nil clears the registration (used by tests).
// Subsequent calls overwrite — the daemon boot path is
// single-threaded so this is safe.
//
// Why RegisterDefaultOps + BootStamps are TWO calls instead of one:
// BootStamps is the load-bearing call site for the boot-time
// metric stamps (issue #573 / ADR-128 restart count + issue #586
// / ADR-129 build info + deploy version + uptime goroutine +
// readiness observer wiring). Those stamps need ops to be fully constructed,
// so they cannot live inside Daemon() (Daemon() is called BEFORE
// fn runs, and ops is constructed INSIDE fn). Splitting them
// keeps BootStamps in the per-daemon run() where ops is alive,
// and RegisterDefaultOps in the same spot so any helper
// goroutine that captures defaultOps (RunAndShutdown's
// drain-flip, recordUptime's 1s ticker) reads the same instance.
func RegisterDefaultOps(ops *OpsMetrics) {
	defaultOps = ops
}

// BootStamps records the daemon's boot-time metrics (issue #573 /
// ADR-128 restart count + issue #586 / ADR-129 build info +
// deploy version + uptime goroutine) and starts the per-second
// uptime and optional OTLP metrics goroutines. Call this immediately after
// `ops := wire.NewOpsMetrics(name)` and before
// wire.RegisterDefaultOps(ops), in the daemon's run() function.
//
// Why hoisted out of Daemon(): the prior wiring had Daemon() call
// defaultOps.* at the top of its body (RecordDaemonRestart +
// SetDaemonBuildInfo + SetDeployVersion + recordUptime spawn +
// MarkReady(name, true, "")). But RegisterDefaultOps runs INSIDE
// fn, so defaultOps was still nil at Daemon()'s call sites — every
// boot stamp was a silent no-op (OpsMetrics methods have
// nil-receiver guards). The whole point of #573/#586 metrics is
// that they fire on every boot; under the prior wiring they fired
// zero times across the entire fleet for years. BootStamps runs
// after ops is constructed, so the stamps actually fire.
//
// The ready-gauge flip deliberately does NOT happen here. It is driven by
// the daemon's ReadyzProbe observer, so daemon_ready is aligned with the
// same dependency and listener checks exposed by /readyz.
//
// A nil ops is tolerated — every helper called below has a
// nil-receiver guard, so a daemon that hasn't wired its
// OpsMetrics (rare — the test path) doesn't panic.
//
// Idempotent on every method called (each Set is a no-op if the
// gauge is already at the desired value). Calling BootStamps
// twice on the same (name, ops) is safe.
func BootStamps(ctx context.Context, name string, ops *OpsMetrics) {
	if ops == nil {
		return
	}
	// Issue #573 / ADR-128: record systemd-driven restart count.
	// The systemd unit (deploy/ansible/roles/<daemon>/files/<daemon>.service)
	// sets Environment=SYSTEMD_RESTARTS_ON_FAILURE=<n> via the
	// RestartCountExport pattern (systemd 254+); when that env
	// var is unset (older systemd, dev runs without a unit, etc.)
	// SystemdRestartCount returns 0 and the counter stays at 0.
	ops.RecordDaemonRestart(name, Version, SystemdRestartCount())

	// Issue #586 / ADR-129: stamp the build info gauge so the
	// "Daemon versions fleet-wide" dashboard panel surfaces the
	// current binary's identity from process start.
	ops.SetDaemonBuildInfo(name, Version, GitSHA, BuildTime)

	// Issue #586 / ADR-129: stamp the platform-wide release
	// identifier gauge (faas_deploy_version{version}).
	ops.SetDeployVersion(Version)

	// Issue #586 / ADR-129: 1-second uptime goroutine. Updates
	// daemon_uptime_seconds{daemon} every 1s until ctx.Done().
	// Cheap — one Prometheus gauge Set per tick per daemon, no
	// allocations beyond the time.Time. Goroutine exits on
	// shutdown; the gauge freezes at the last value (which is
	// the right thing — operators want to see "uptime at the
	// moment of shutdown" rather than a zero).
	go recordUptimeWithOps(ctx, name, time.Now(), ops)
	startOTLPMetrics(ctx, name, ops)
}

// startOTLPMetrics binds the optional OTLP metrics exporter to the same
// registry used by /metrics. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT takes
// precedence; the generic OTLP endpoint is the fallback shared with traces.
// The exporter is deliberately best-effort so a collector outage cannot take
// a serving daemon down. The bridge also registers exporter-health metrics
// when no endpoint is configured, making an intentional disable explicit.
func startOTLPMetrics(ctx context.Context, serviceName string, ops *OpsMetrics) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if ops == nil {
		return
	}
	shutdown, err := startOTLPMetricsWithPrefix(ctx, ops.Registry(), endpoint, serviceName, ops.metricPrefix, slog.Default())
	if err != nil {
		slog.Default().Warn("otlp metrics bridge disabled", "service", serviceName, "err", err)
		return
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdown(shutdownCtx); shutdownErr != nil {
			slog.Default().Warn("otlp metrics final export failed", "service", serviceName, "err", shutdownErr)
		}
	}()
}

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

	// Issue #852: do NOT stamp "daemon" on Logger().With — slog.Logger.With
	// accumulates attrs without dedup, so NewCorrelationLogger (called below)
	// would emit the daemon name twice on every record. NewCorrelationLogger
	// injects FieldDaemon once when daemon != "". Keep "version" on the With
	// chain so the version stamp survives correlation envelope construction.
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

	// Issue #573 / ADR-128 + #586 / ADR-129: boot-time metric
	// stamps moved out of Daemon() into wire.BootStamps. They used
	// to live here, but RegisterDefaultOps runs INSIDE fn, so
	// defaultOps was still nil at these call sites and every
	// stamp was a silent no-op. BootStamps fires them from the
	// daemon's run() right after ops is constructed — see
	// wire.BootStamps.

	log.Info("starting", "config", *configPath, "restart_count", SystemdRestartCount(), "git_sha", GitSHA, "build_time", BuildTime)
	// Issue #852: do NOT re-stamp "daemon" / "version" on this call site.
	// The slog.Logger.With envelope on log already carries "version" and
	// NewCorrelationLogger already carries "daemon" — re-passing them here
	// would emit duplicate JSON keys per the slog JSON handler contract.
	// Readiness is emitted by the daemon's ReadyzProbe observer after its
	// dependencies and serving listener are healthy; process start is not
	// equivalent to readiness.
	log.Info("started")
	if err := fn(ctx, log); err != nil {
		log.Error("exited with error", "err", err)
		// Issue #586 / ADR-129 (Finding 2 from PR #1091 review):
		// flip daemon_ready{daemon} to 0 BEFORE os.Exit so a scrape
		// that lands during the SIGTERM drain window sees 0, not
		// "ready forever, then process gone". defaultOps is nil-safe
		// so a daemon that exited before RegisterDefaultOps ran
		// doesn't panic.
		defaultOps.MarkReady(name, false, "exited with error")
		os.Exit(1)
	}
	// Issue #586 / ADR-129 (Finding 2 from PR #1091 review):
	// flip daemon_ready{daemon} to 0 BEFORE the process exits so
	// the §12 "Fleet readiness" panel reflects the post-serve
	// state. Without this flip the gauge stays at 1 forever
	// (until the process disappears from the scrape target),
	// producing a misleading dashboard panel that shows
	// "every daemon ready" while the fleet is mid-shutdown.
	defaultOps.MarkReady(name, false, "shutdown complete")
	log.Info("shutdown complete")
}

// recordUptimeWithOps (issue #586 / ADR-129) ticks
// daemon_uptime_seconds every 1s. Lives in the goroutine spawned
// from wire.BootStamps; exits when ctx is cancelled. Uses the
// ops argument explicitly (NOT defaultOps) so BootStamps's
// "ops is alive at call time" invariant is preserved — callers
// pass the same *OpsMetrics NewOpsMetrics returned. Time-since-start
// is computed locally each tick so a clock-slew doesn't
// accumulate error. A nil ops is tolerated (no-op).
func recordUptimeWithOps(ctx context.Context, name string, startedAt time.Time, ops *OpsMetrics) {
	if ops == nil {
		return
	}
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ops.SetDaemonUptime(name, time.Since(startedAt).Seconds())
		}
	}
}

// SystemdRestartCount returns the systemd-driven restart count
// for the current process, or 0 if the env var
// $SYSTEMD_RESTARTS_ON_FAILURE is unset / unparseable. The systemd
// unit's Restart=on-failure + RestartCountExport pattern (systemd
// 254+) sets this env var on every restart; absence is a benign
// signal of either an older systemd or a dev run without a unit.
// Callers use the value to populate OpsMetrics.daemonRestartCount
// at boot — see cmd/vmmd/main.go and the other cmd/<daemon>/main.go
// files for the call sites (issue #573 / ADR-128).
func SystemdRestartCount() int {
	n, err := strconv.Atoi(os.Getenv("SYSTEMD_RESTARTS_ON_FAILURE"))
	if err != nil || n < 0 {
		return 0
	}
	return n
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
