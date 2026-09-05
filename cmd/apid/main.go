// Command apid — control API (spec §4.2).
//
// apid is the public REST API, the auth boundary, and the ONLY writer to
// customer-intent tables (accounts, apps, deployments, domains). It validates
// plan quotas before any work happens and never calls vmmd/builderd directly —
// it writes rows and notifies owners via pg_notify (spec §Component ownership).
//
// M5+: apid uses the pgx-backed state.PgStore against the same Postgres
// cluster schedd/imaged share; queries.sql is the SQL source of truth and
// pgstore.go adapts the result shape to the domain types. The CLI exercises
// apid through FAAS_DEV_TOKEN for local dev (memstore seed path stays for
// tests).
package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/authcode"
	billingloader "github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/eventretention"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/logintoken"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedDevAccount creates (or finds) the dev@local Free account. The
// API-key mint that used to live here is REMOVED: production boxes
// never set FAAS_DEV_TOKEN (they use the real signup flow + gregale
// sign-keys init), and dev/test environments mint keys explicitly via
// pkg/e2etest.Harness.SeedAccount or their own CLI setup. Calling
// CreateAPIKey on every apid boot was a pgstore.Write at restart-loop
// frequency — the path that crash-looped apid in run 31121004495
// (post PR #633 deploy 2026-08-04). Removing it sidesteps the
// restart-cycle race entirely.
//
// Tier A7 PR-D.
func seedDevAccount(ctx context.Context, store state.Store, token string) error {
	if !api.ValidAPIKeyFormat(token) {
		return fmt.Errorf("FAAS_DEV_TOKEN is not a valid API key (want %s… format)", api.APIKeyPrefix)
	}
	acct, err := store.AccountByEmail(ctx, "dev@local")
	if errors.Is(err, state.ErrNotFound) {
		// Intentionally NOT routed through CreateAccountWithPersonalOrg — the
		// dev bootstrap is a synthetic local-only fixture. Production
		// signup paths (postSignup, OAuth callbacks, CLI activation) wrap
		// every account through the helper; the dev bootstrap is opt-out by
		// design. The PR 3 backfill (migrations/00101) and the e2e harness
		// SeedAccount cover the "every account has a personal org"
		// invariant.
		if _, err = store.CreateAccount(ctx, "dev@local", api.PlanFree); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_ = acct // find-or-create confirmed; the row exists either way
	return nil
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// rekeyEnabledFromEnv reads FAAS_REKEY_ENABLED via the test seam
// (deps.getenv) and parses the canonical truthy spellings.
// Default false preserves the v1 no-op posture — operators who
// have not yet rotated the host identity see exactly zero rekey
// activity (no file write, no goroutine, no audit noise).
//
// Why a dedicated helper (vs os.LookupEnv in main.go): the flag
// is consumed at boot to decide whether to construct a Runner,
// and the parsing rules need to live in one place so a unit test
// for the parsing can pin the truthy set without booting the
// daemon. Same shape as graceIntervalFromEnv (line 529).
//
// The truthy spelling set lives in rekey_runner.go as
// rekeyTruthyLiterals — name-spaced there so goconst doesn't tie
// this subsystem to deploy_inputs.go's checkbox literals.
func rekeyEnabledFromEnv(getenv func(string) string) bool {
	v := strings.ToLower(strings.TrimSpace(getenv(rekeyEnabledEnvVar)))
	for _, lit := range rekeyTruthyLiterals {
		if v == lit {
			return true
		}
	}
	return false
}

// dataPlacementEnabledFromEnv reads FAAS_DATA_PLACEMENT via the
// test seam (deps.getenv) and parses the canonical truthy
// spellings. Default false preserves the v1 no-op posture —
// pre-PR-B operators see exactly zero data-upstream handler
// activity (no row INSERT, no audit kind, no pg_notify trigger
// trip). Mirrors rekeyEnabledFromEnv's shape.
func dataPlacementEnabledFromEnv(getenv func(string) string) bool {
	v := strings.ToLower(strings.TrimSpace(getenv("FAAS_DATA_PLACEMENT")))
	for _, lit := range rekeyTruthyLiterals {
		if v == lit {
			return true
		}
	}
	return false
}

// resolveMetricsAddr reads FAAS_APID_METRICS_ADDR via the test seam
// (deps.getenv). Empty string disables the listener (this is the
// deliberately-distinct envOr path: envOr() collapses empty→unset→
// fallback, which is right for FAAS_APID_LISTEN but wrong here,
// where empty means "skip the listener entirely"). The test
// seam + the explicit-empty-disable semantic are the reasons this
// helper exists as a package-level function (vs a Config.Get
// metricsAddrDefault is the loopback bind address for the /metrics
// listener. The default is loopback so an operator typo (or a
// missing env var in prod) can't accidentally expose the internal
// registry to the public network — series like apid_ops_total{op,code}
// leak auth-rejection rates and per-route traffic shape (review
// finding #1 on PR #132). Loopback bind is safe because the local
// Prometheus scrapes from the box itself. Mirrors cmd/builderd/main.go's
// MetricsAddr pattern (PR #124).
//
// PR-0 (issue #678): the literal was repeated 3+ times across the
// pre-PR-0 inline env reads, the post-PR-0 resolveMetricsAddr helper,
// and the package-level default. Extracted to a const for goconst
// (golangci-lint v2.4.0 fires at 3 occurrences).
const metricsAddrDefault = "127.0.0.1:9101"

// prometheusURLDefault is the control-plane-local Prometheus endpoint
// installed by the Ansible role. Single-box and test callers can still
// leave FAAS_PROMETHEUS_URL empty; only a role-correct control plane gets
// the production default, so unit/e2e fixtures retain their explicit
// degraded-without-Prometheus behavior.
const prometheusURLDefault = "http://127.0.0.1:9095"

func resolvePrometheusURL(getenv func(string) string, boxRole role.Role) string {
	if v := getenv("FAAS_PROMETHEUS_URL"); v != "" {
		return v
	}
	if boxRole == role.RoleControlPlane {
		return prometheusURLDefault
	}
	return ""
}

// resolveMetricsAddr reads FAAS_APID_METRICS_ADDR via the test
// seam (deps.getenv). Empty string disables the listener (the
// deliberately-distinct envOr path: envOr() collapses empty→unset→
// fallback, which is right for FAAS_APID_LISTEN but wrong here,
// where empty means "skip the listener entirely"). The test
// seam + the explicit-empty-disable semantic are the reasons this
// helper exists as a package-level function (vs a Config.Get
// method). Defaults to metricsAddrDefault so an operator typo (or
// a missing env var in prod) can't accidentally expose the
// internal registry to the public network.
//
// The e2e harness stamps `FAAS_APID_METRICS_ADDR=` to avoid the
// metricsAddrDefault bind race against a sibling or zombie apid
// run.
//
// PR-0 (issue #678): the tomlDefault argument pulls the default
// from cfg.GetMetricsAddr so a TOML-configured metrics_addr is
// respected (issue #678 PR-0 — apid's first ever TOML config).
// When tomlDefault is non-empty, it wins over the package-level
// of metricsAddrDefault.
func resolveMetricsAddr(getenv func(string) string, tomlDefault string) string {
	v := getenv("FAAS_APID_METRICS_ADDR")
	if v == "" && tomlDefault == "" {
		return metricsAddrDefault
	}
	if v == "" {
		return tomlDefault
	}
	return v
}

// gatewaydControlURLDefault is the loopback URL apid dials when
// FAAS_GATEWAYD_CONTROL_URL is unset. Mirrors gatewayd-internal's
// default ControlAddr (cmd/gatewayd-internal/config.go:181
// 127.0.0.1:9090, scheme added). Same-box is the only supported
// posture today; cross-box deployments override via the env
// var. Extracted to a const so goconst pins it to one occurrence
// (the helpers below + the server field comment at
// cmd/apid/server.go:58 cite the same literal).
const gatewaydControlURLDefault = "http://127.0.0.1:9090"

// resolveGatewaydControlURL reads FAAS_GATEWAYD_CONTROL_URL via
// the test seam (deps.getenv) and applies the loopback default
// when the env value is empty. Same shape as resolveMetricsAddr
// — the test seam keeps macOS-dev + CI from trying to dial a
// real gatewayd unless the test opted in via WithGatewaydControlURL,
// and the default keeps a same-box prod install from bricking
// the per-route surface just because the operator never exported
// the env var.
//
// Distinction from resolveMetricsAddr: there is no explicit-empty
// "disable" semantic for FAAS_GATEWAYD_CONTROL_URL. The dial
// surface either works (operator wired it OR default loopback
// reachable) or the upstream dial fails and getAppRoutes renders
// the unavailable state — there is no third "operator opted out
// of the surface entirely" position.
func resolveGatewaydControlURL(getenv func(string) string) string {
	v := getenv("FAAS_GATEWAYD_CONTROL_URL")
	if v == "" {
		return gatewaydControlURLDefault
	}
	return v
}

// resolveAdvisorySock reads FAAS_APID_ADVISORY_SOCK via the test
// seam (deps.getenv). Empty string disables the listener. Tests
// disable by default (their getenv stub returns "" for unknown
// keys) so macOS dev boxes don't try to bind /run/faas
// (read-only on macOS). Production wires defaultDeps.getenv to
// os.Getenv; the systemd unit stamps FAAS_APID_ADVISORY_SOCK=
// /run/faas/apid.sock explicitly so the default doesn't matter
// in prod — the explicit assignment is what enables the
// listener there.
//
// PR-0 (issue #678): Config.GetAdvisorySock (cmd/apid/config.go)
// is the TOML-aware version. main.go calls the helper directly
// when a Config is in scope; this env-only helper stays for the
// test seam that doesn't yet thread a Config (the existing
// tests pass nil cfg → this branch). Tests that build a real
// Config use Config.GetAdvisorySock directly.
func resolveAdvisorySock(getenv func(string) string, cfg *Config) string {
	if cfg != nil {
		return cfg.GetAdvisorySock(getenv)
	}
	return getenv("FAAS_APID_ADVISORY_SOCK")
}

// resolveGithubdBridgeSock reads FAAS_APID_GITHUBD_BRIDGE_SOCK via
// the test seam (deps.getenv). Empty string disables the listener —
// the same pattern as resolveAdvisorySock so macOS dev boxes
// don't try to bind /run/faas. The systemd unit stamps
// FAAS_APID_GITHUBD_BRIDGE_SOCK=/run/faas/apid-githubd.sock in
// production (issue #432 phase 5). Unlike the advisory socket,
// the bridge socket has a separate path because the consumer
// (githubd) dials it, not vmmd, and the 0660 DAC group is shared
// between the githubd user and apid user.
//
// PR-0 (issue #678): same thin-wrapper pattern as resolveAdvisorySock.
func resolveGithubdBridgeSock(getenv func(string) string, cfg *Config) string {
	if cfg != nil {
		return cfg.GetGithubdBridgeSock(getenv)
	}
	return getenv("FAAS_APID_GITHUBD_BRIDGE_SOCK")
}

// resolveGithubdStagingRoot reads FAAS_GITHUBD_WORK_DIR via the
// test seam (deps.getenv). The default matches the githubd-side
// githubdWorkDir() default (/var/lib/faas/githubd); the bridge
// handlers append /build-sources internally so the same env var
// that githubd reads configures the apid-side allowlist. The
// staging prefix is the directory githubd's
// pkg/githubd/staging.go:72 writes per-app tarballs into; a
// mismatch between the two sides surfaces as invalid_argument on
// the first push and is logged + retried by the dispatcher (the
// githubd-side WARN carries the staged path the gRPC call
// returned).
func resolveGithubdStagingRoot(getenv func(string) string) string {
	if p := getenv("FAAS_GITHUBD_WORK_DIR"); p != "" {
		return p
	}
	return "/var/lib/faas/githubd"
}

// runDeps is the DI seam for run — same pattern as vmmd / gatewayd-internal so we can
// exercise the listener lifecycle without binding :8081 from tests.
type runDeps struct {
	listen func(network, addr string) (net.Listener, error)
	store  func() state.Store
	notif  func() Notifier
	getenv func(string) string
	// newSrv builds the customer-facing http.Server (issue #995
	// Phase 1 — receives the resolved timeouts via the *Config
	// argument rather than a bare addr/handler pair so the listener
	// hardening is applied at construction time, not after the
	// fact).
	newSrv   func(addr string, h http.Handler, cfg *Config) *http.Server
	bgBefore func(ctx context.Context, log *slog.Logger, srv *server) // optional pre-listen hook (e.g. DNS poller)
	loginTTL time.Duration                                            // dashboard magic-link expiry
	// mailer is the outbound email sender (gap G4). Nil means "pick
	// from env at startup" via mail.SenderFromEnv — same pattern meterd
	// uses (cmd/meterd/main.go:82-87). Tests inject a stub.
	mailer mail.Sender
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check (the test runner doesn't carry
	// the production capset, and MustCheckOnBoot calls os.Exit
	// on violation).
	capCheck func() error
	// PR-P2: TOML config wiring. configPath defaults to
	// /etc/faas/apid.toml in defaultDeps; tests override to point at
	// a temp file with a hand-rolled [billing] block.
	configPath string
	// loadBillingConfig reads the [billing] block from apid.toml.
	// nil in production (defaultDeps wires billingloader.LoadBillingConfigFromPath);
	// tests stub to return a hand-rolled *RootBillingConfig.
	loadBillingConfig func(path string) (*billingloader.RootBillingConfig, error)
	// PR-0 (issue #678): loadConfig reads the apid config (the
	// non-billing surface: ListenAddr, MetricsAddr, AdvisorySock,
	// GithubdBridgeSock, GithubdSocket, AppsDomain, the three TLS
	// clusters, NodeName). nil in production (defaultDeps wires
	// LoadConfig); tests stub to return a hand-rolled *Config.
	// Same file as loadBillingConfig consumes — the two readers
	// share /etc/faas/apid.toml.
	loadConfig func(path string) (*Config, error)
	// PR-0 (issue #678): preLoadedConfig, when non-nil, skips the
	// LoadConfig call inside run() and uses this Config directly.
	// Tests inject a hand-rolled *Config to assert behaviour
	// without round-tripping a TOML file through LoadConfig. nil
	// in production.
	preLoadedConfig *Config
	// PR-0 (issue #678): config carries the loaded *Config into
	// runWithDeps so the listener / dial / TLS helpers can read
	// cfg.Load* / cfg.Get* directly. Set by run() immediately after
	// LoadConfig returns; nil in the test seam that constructs
	// runDeps directly (those tests should set preLoadedConfig
	// instead).
	config *Config
	// ADR-094: closePool is the pool-cleanup hook run() wires after
	// db.Open succeeds. runWithDeps calls it on every early-return
	// between db.Open and the post-bind defer-install (so an error
	// anywhere in the bind path closes the pool out, not the
	// in-flight bgBefore goroutines). Tests can inject a no-op
	// (`func() {}`) so the existing TestRunWithDeps_ListenErrorReturns
	// shape — which never sets up a real pool — keeps working.
	// nil in production before run() sets it.
	closePool func()
	// PR-B (issue #678 / ADR-056): pool is the *pgxpool.Pool run() opens
	// via db.Open. runWithDeps needs it to construct the handshake-
	// layer PGNodeVerifier (wire.NewPGNodeLoader(pool)) and to wire
	// its notification drain (db.SubscribeWithReconnect(ctx, pool,
	// [db.NotifyComputeNodeChanged], log)). Mirrors the existing
	// deps.store / deps.notif pattern: production wires it from run();
	// tests that build runDeps directly without a real pool leave it
	// nil — the verifier block below short-circuits to nil when pool
	// is nil (the cfg.NodeName == "" production path AND the
	// pre-PR-B test paths). nil in production before run() sets it.
	pool *pgxpool.Pool
	// PR-B (issue #678 / ADR-056): preLoadedNodeVerifier lets tests
	// inject a stub wire.NodeVerifier without booting Postgres or
	// wiring a real PGNodeLoader. nil in production (the
	// cfg.NodeName != "" block in runWithDeps owns the
	// wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log) path);
	// nil-tolerant — the cfg.NodeName gate still runs, so a test
	// that sets only preLoadedConfig and preLoadedNodeVerifier == nil
	// keeps the single-box wire behaviour.
	preLoadedNodeVerifier wire.NodeVerifier
	// PR-B (issue #678 / ADR-056): captureDialTLS is a test-side hook
	// invoked at every Load*TLSWithVerifier dial site with (name,
	// *tls.Config). name is "githubd", "advisory", or "bridge".
	// nil in production; nil-tolerant (runWithDeps no-ops on nil).
	// Lets the TestRunWithDeps_PassesNodeVerifierToDialSites pin test
	// assert that a non-nil preLoadedNodeVerifier propagates into
	// all three tls.Cfg.VerifyPeerCertificate hooks without booting
	// a real Postgres pool.
	captureDialTLS func(name string, cfg *tls.Config)
}

func defaultDeps() runDeps {
	return runDeps{
		listen: net.Listen,
		store:  func() state.Store { return state.NewMemStore() },
		notif:  func() Notifier { return noopNotifier{} },
		getenv: os.Getenv,
		// Issue #995 Phase 1: harden the customer-facing http.Server
		// with ReadTimeout / WriteTimeout / IdleTimeout / MaxHeaderBytes
		// pulled from the resolved *Config (env overlay wins over TOML
		// wins over defaults). ReadHeaderTimeout is unchanged (10s — the
		// legacy defence on header arrival).
		newSrv: func(addr string, h http.Handler, cfg *Config) *http.Server {
			env := os.Getenv
			if cfg == nil {
				cfg = &Config{}
			}
			return &http.Server{
				Addr:              addr,
				Handler:           trace.HTTPHandler("apid", h),
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       cfg.GetRequestReadTimeout(env),
				WriteTimeout:      cfg.GetRequestWriteTimeout(env),
				IdleTimeout:       cfg.GetRequestIdleTimeout(env),
				MaxHeaderBytes:    int(cfg.GetRequestMaxHeaderBytes(env)),
			}
		},
		loginTTL:          15 * time.Minute,
		configPath:        "/etc/faas/apid.toml",
		loadBillingConfig: billingloader.LoadBillingConfigFromPath,
		loadConfig:        LoadConfig,
	}
}

func main() {
	wire.Daemon("apid", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	deps := defaultDeps()

	// DEPLOY-1 / ADR-075 capdecl gate. apid's capsDecl is
	// cap_net_bind_service (HTTPS listener). A misconfigured
	// AmbientCapabilities line fails fast at boot. The
	// capCheck seam lets tests stub the live /proc/self/status
	// check (review finding M2 — every daemon now has this).
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}

	// Production-leveling Stream C: env-scoped stuck-after
	// threshold. Read FAAS_SAFEDEPLOY_STUCK_AFTER before the
	// recovery routes wire up so the very first
	// /v1/apps/{slug}/rollouts/recover request uses the
	// overridden window. Returns 0 on unset / unparseable —
	// the setter silently ignores <= 0 so the canned 30min
	// stays in effect (no silent inversion).
	state.SetRecoverRolloutStuckAfter(stuckAfterFromEnv(log))

	// Load the same TOML that supplies the listener and TLS settings before
	// opening Postgres. This is important on split-box control planes: the
	// renderer writes a local Unix-socket db_url, while compute boxes keep
	// their password-authenticated TCP DSN in compute-db.env.
	cfg := deps.preLoadedConfig
	var err error
	if cfg == nil {
		cfg, err = deps.loadConfig(deps.configPath)
		if err != nil {
			return fmt.Errorf("apid: load config: %w", err)
		}
	}

	// Gate-B box-role gate. apid is a control-plane daemon — it refuses to
	// start under RoleComputeOnly. LoadConfig applies the TOML/env role
	// precedence before this check; default is RoleSingleBox so single-box
	// dev boots unmoved. The gate still runs before db.Open.
	if err := role.Require("apid", cfg.Role,
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}

	pool, err := db.Open(ctx, cfg.DBURL)
	if err != nil {
		return fmt.Errorf("apid: open db: %w", err)
	}
	// ADR-094: the pool's lifetime is no longer bound to run()'s
	// defer. The pre-bind goroutines in bgBefore (rekey walker,
	// sseFanIn, audit subscriber, grace sweep, etc.) each call
	// pool.Acquire(); an early-return anywhere between here and
	// the listener bind used to close the pool out from under
	// those goroutines — they returned "closed pool" and main
	// never reached the listener-bind log line. The fix is the
	// closePool helper + the explicit closePool() call before
	// every early-return in this range, with a single
	// defer closePool() at the post-bind site (below the metrics
	// / advisory / bridge listener sections, just before
	// srv.Serve).
	//
	// ADR-094 pins this shape via pkg/db/warmup_architecture_test.go
	// so a future refactor that drops closePool() at an early-return
	// site fails the test instead of silently reintroducing the
	// race.
	closePool := func() {
		if pool != nil {
			pool.Close()
		}
	}
	// Warm-up barrier: acquire (and release) 4 connections before
	// bgBefore launches its goroutines. This is the belt-and-braces
	// defence — proves the pool can serve N parallel connections
	// before any one of them races another for the same slot. The
	// "N=4" default matches apid's expected boot fan-out (audit
	// subscriber Subscribe + rekey first-walk + sseFanIn Subscribe +
	// grace/login/eventretention first-pass DELETE each grab one
	// connection at startup; 4 is the upper bound for any single
	// tick). If a future daemon picks up a fifth concurrent
	// goroutine at boot, bump the constant here + update the
	// architecture test's expected ordering.
	if err := db.WarmUp(ctx, pool, 4, 5*time.Second); err != nil {
		closePool()
		return fmt.Errorf("apid: pool warm-up: %w", err)
	}
	// F2 / ADR-124 / PR-2 audit: db.MigrateUp acquires the session-scoped
	// pg_advisory_lock internally, so apid's boot is safe alongside every
	// other daemon in the fleet. Do NOT replace this with a direct goose.Up
	// call — the lock is the load-bearing guarantee.
	if err := db.MigrateUp(ctx, pool); err != nil {
		closePool()
		return fmt.Errorf("apid: migrate: %w", err)
	}

	// Issue #249 / spec §11: gate Strict-Transport-Security on
	// FAAS_HSTS_ENABLED. Default true; dev mode can flip to false.
	// RFC 6797 §7.2 says UAs ignore HSTS on plain HTTP, so the knob
	// is purely cosmetic — but emitting it on a dev plaintext loop
	// back listener confuses operators reading the headers.
	httpsec.SetHSTSEnabled(httpsec.HSTSEnabledFromEnv(os.Getenv))

	deps.store = func() state.Store { return state.NewPgStore(pool) }
	deps.config = cfg
	// Mega-PR-A (issue #911 / ADR-110 PR-1): boot log carrying the
	// multi-box identity so an operator reading the systemd journal
	// can map the daemon to the right compute_node row. Mirrors the
	// schedd owner-node line so the playbook shape is identical
	// across daemons.
	if cfg.NodeName != "" {
		log.Info("apid owner node", "node_name", cfg.NodeName)
	} else {
		log.Info("apid: legacy single-box (cfg.NodeName empty)")
	}
	deps.notif = func() Notifier { return pgNotifier{pool: pool} }
	// ADR-094: hand runWithDeps the same closePool helper so the
	// post-bind defer (installed just before srv.Serve) and every
	// pre-bind early-return close the pool consistently. Tests that
	// build runDeps directly without a pool pass a no-op closure.
	deps.closePool = closePool
	// PR-B (issue #678 / ADR-056): hand runWithDeps the same
	// *pgxpool.Pool so the handshake-layer PGNodeVerifier
	// construction block can use wire.NewPGNodeLoader(pool) and
	// db.SubscribeWithReconnect(ctx, pool, ...). nil in tests that
	// build runDeps directly without a real pool — the verifier
	// block short-circuits to nil when pool is nil.
	deps.pool = pool
	deps.bgBefore = func(ctx context.Context, log *slog.Logger, srv *server) {
		go srv.runObjectStorageRecovery(ctx)
		go srv.runObjectStorageAccounting(ctx)
		// ADR-132: pg_notify is a low-latency wake-up only. The
		// subscriber re-reads the durable runtime_config_entries row, so a
		// missed notification is repaired by the next reconnect or boot.
		go func() {
			if err := runRuntimeConfigSubscriber(ctx, pool, srv, log); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("runtime_config subscriber exited", "err", err)
			}
		}()
		// ADR-089 PR-C — background re-seal runner. The runner is
		// nil when FAAS_REKEY_ENABLED is unset (or when identities
		// failed to load); we skip the goroutine launch in that
		// case so the boot path is a no-op on the default deploy.
		// Mirrors the launch pattern of every other background
		// worker below (`go func() { _ = thing.Run(ctx) }()`)
		// — error is discarded at the goroutine boundary, the
		// run blocks until ctx.Done() (Runner.Run signature,
		// rekey_runner.go).
		if srv.rekeyRunner != nil {
			go func() { _ = srv.rekeyRunner.Run(ctx) }()
		}
		// Move 3 (M7.5 prep): bridge pg_notify → in-process broadcaster.
		// Runs as a background goroutine for the daemon's lifetime; the
		// SubscribeWithReconnect wrapper reconnects across Postgres
		// restarts. Fails fast at boot if the initial Subscribe errors
		// — the dashboard SSE surfaces the gap rather than silently
		// producing empty frames. Lives in this closure (not runWithDeps)
		// because production-run holds the *pgxpool.Pool and the test
		// seam in runWithDeps doesn't.
		go sseFanIn(ctx, log, pool, srv.events, nil)
		startDNSPoller(ctx, srv, log)
		// ADR-127 PR-B: regression detector. Mirrors the dns_poller's
		// shape — first-pass-immediate + ticker + ctx-cancel. The
		// cron is gated on FAAS_REQUEST_TELEMETRY_ENABLED so the
		// surface can be dark-launched with the ginstal kill-switch
		// that the apid gRPC receiver (Stage 4) already honors.
		startDebugRegressionCron(ctx, srv, log, deps.getenv)
		// G6 grace timer (spec §17 G6, ADR-021): the 30-day deletion
		// grace sweep lives in apid (not meterd) because the write
		// side (DELETE /v1/account, POST /v1/account/restore) is here
		// and meterd owns quotas/billing only. Default Interval 60s
		// matches the grace-side precision we need; sweep is a
		// ListAllAccounts walk so it stays bounded by the customer
		// count on the one box.
		graceLoop := grace.New(grace.Params{
			Store:    srv.store,
			Mailer:   graceSenderAdapter{m: srv.mailer},
			Log:      log,
			Interval: graceIntervalFromEnv(log),
			Notif: func(ctx context.Context, ch, payload string) error {
				return srv.notif.Notify(ctx, ch, payload)
			},
			Audit: srv.audit, // issue #755 / PR-5.5: emit account.deleted from the sweep
		})
		go func() { _ = graceLoop.Run(ctx) }()
		// Login-token cleanup (issue #165 PR #2, ADR-032). The
		// login_tokens table backs password-reset (15-min TTL) and
		// the legacy magic-link surface PR #1 removed. The
		// /login/forgot → POST /auth/reset pair is the only
		// production caller — we run a 24h ticker so the table
		// stays bounded by (rate of reset requests) × 15min.
		// pkg/logintoken mirrors pkg/grace (same Run / RunOnce
		// shape) so the lifecycle is consistent with the G6 grace
		// timer above.
		loginTokenCleanup := logintoken.New(logintoken.Params{
			Store: srv.store,
			Log:   log,
		})
		go func() { _ = loginTokenCleanup.Run(ctx) }()
		// ADR-075: 90-day audit retention. The events table grows
		// ~3-4 GB/year/active-tier through the auth / key / secret
		// / account / stateless audit namespaces plus the future
		// wake-timeline / sidecar surfaces. The daily loop trims
		// rows older than eventretention.DefaultCutoffDays (90d,
		// SOC 2 CC6.2 evidence-retention floor). Mirrors
		// pkg/logintoken byte-for-byte (same Run / RunOnce pattern,
		// same first-pass-error defence-in-depth).
		eventRetentionCleanup := eventretention.New(eventretention.Params{
			Store: srv.store,
			Log:   log,
		})
		// ADR-091 D20.3 / PR-B residual: thread the Ops into the
		// audit-event retention cleanup loop. srv.ops is populated
		// by srv.WithOpsMetrics(ctx, ops) on line ~1027 BEFORE
		// deps.bgBefore is invoked (line 1248), so reading srv.ops
		// here is safe; the closure's goroutine launch proceeds
		// regardless of whether ops is nil (the loop runs and
		// logs but does not increment — same trade-off the apid
		// audit auditor makes with pkg/audit.Auditor.SetOps).
		eventRetentionCleanup.SetOps(srv.ops)
		go func() { _ = eventRetentionCleanup.Run(ctx) }()
		// Issue #562 / PR-A: log archive shipper. Walks the local
		// spool dir every cfg.FlushInterval (5 min default) and
		// gzip-PUTs .partial files to s3://{bucket}/faas-logs/{instance}/
		// {YYYY}/{MM}/{DD}.jsonl.gz. Disabled (returns nil on ctx
		// cancel) when FAAS_LOG_ARCHIVE_BUCKET is unset — the
		// pgAdmin-style on-box ring buffer is the only log surface
		// for Free + Hobby tiers in that mode.
		//
		// Credentials are read from the archive-creds.json envelope
		// unsealed by `gregale backup unseal-archive-creds` and
		// mounted at the systemd credential path. On a host that
		// hasn't been unsealed yet the optional credential is absent
		// and the shipper remains disabled.
		shipCfg, shipErr := logarchive.ConfigFromEnv(os.Getenv, log)
		if shipErr == nil {
			archiveCredsPath := envOr(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
			if creds, err := logarchive.ReadCredentials(archiveCredsPath); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Warn("logarchive.creds_unavailable", "path", archiveCredsPath, "err", err)
				}
			} else {
				shipCfg = shipCfg.WithCredentials(creds)
			}
		}
		switch {
		case shipErr != nil:
			log.Warn("logarchive.config_failed", "err", shipErr)
		case !shipCfg.Enabled():
			log.Info("logarchive.disabled", "reason", "FAAS_LOG_ARCHIVE_BUCKET unset")
		default:
			s3, err := logarchive.NewS3Client(
				shipCfg.Endpoint,
				shipCfg.Region,
				shipCfg.Bucket,
				shipCfg.KeyID,
				shipCfg.Secret,
			)
			if err != nil {
				log.Warn("logarchive.s3client_init_failed", "err", err)
			} else {
				spool := logarchive.NewSpool(shipCfg.SpoolRoot, shipCfg.LocalBytesMax)
				shipMetrics := logarchive.NewMetrics(srv.ops.Registry())
				sh, err := logarchive.NewShipper(shipCfg, spool, s3, log, shipMetrics)
				if err != nil {
					log.Warn("logarchive.shipper_init_failed", "err", err)
				} else {
					go func() {
						if err := sh.Run(ctx); err != nil && ctx.Err() == nil {
							log.Warn("logarchive.run_returned_error", "err", err)
						}
						_ = sh.Spool().CloseAll()
					}()
				}
			}
		}
		// Issue #300: topNSampler drives the apid_top_tenant_rps
		// gauge from the rolling per-account count fed by
		// observeWrap (server.go:observeWrap). 5s tick; runs for
		// the daemon's lifetime; stops cleanly on ctx cancel.
		topNSampler := newTopNSampler(srv.ops, log)
		go topNSampler.run(ctx)
		// Issue #250: pgBackupPushedSampler drives the
		// apid_pg_backup_last_pushed_seconds gauge from the mtime
		// of the newest tarball in /var/lib/pgsql/basebackup/.
		// 60s tick (matches the PgBackupStale alert's `for: 5m`
		// window — at least 5 fresh ticks per evaluation).
		pgBackupPushedSampler := newPgBackupPushedSampler(srv.ops, log)
		go pgBackupPushedSampler.run(ctx)
		// Webhook replay-dedupe sweep (issue #294). The
		// webhook_deliveries table is written by all three ingresses
		// (GitHub via gatewayd-internal, Stripe + Paddle via apid); the TTL
		// expires_at column + the partial index keep the per-tick
		// DELETE bounded by (60s tick × ~rows added in that window).
		// 60s matches the meterd dunning sweep cadence.
		webhookSweeper := webhookdedupe.NewSweeper(webhookdedupe.DefaultSweepInterval)
		go func() { _ = webhookSweeper.Run(ctx) }()
		// Issue #472 / ADR-058: bridge the `audit_event` pg_notify
		// channel (imaged emits signature-failure events via this
		// channel since imaged is the only fire-and-forget audit
		// publisher that doesn't write the events table directly)
		// into the apid-side auditor. Subscribing via
		// SubscribeWithReconnect is the same pattern schedd's
		// deletion_subscriber uses (pkg/db/notify.go:304) so the
		// daemon survives Postgres restarts. The initial Subscribe
		// error is fatal — silent drop is the bug we're closing.
		if srv.audit != nil {
			go func() {
				// issue #517 / PR-C / ADR-064: thread the
				// events Platform through the audit
				// subscriber so verify-rejection kinds
				// (app.signature_invalid /
				// app.signature_missing) also emit the
				// typed wake.deploy_failed row.
				if err := runAuditSubscriber(ctx, pool, srv.audit, log, srv.eventsPlatform); err != nil && ctx.Err() == nil {
					log.Error("audit: subscriber exited", "err", err)
				}
			}()
		}
		// Public-release hardening: pg_notify is only the wakeup for
		// imaged signature audits. The durable outbox replay loop
		// recovers rows missed during a LISTEN reconnect or an apid
		// restart, and also drains rows when the notification subscriber
		// is unavailable.
		if _, ok := srv.store.(state.AuditEventOutboxStore); ok {
			go func() {
				if err := runAuditOutbox(ctx, srv.store, log, srv.eventsPlatform); err != nil && ctx.Err() == nil {
					log.Error("audit: durable outbox exited", "err", err)
				}
			}()
		}
		// ADR-126 / issue #975 item #2: bridge the two pg_notify
		// channels that mutate the `?source=auto` cache inputs
		// (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged)
		// into SpecCache.InvalidateByApp. Nil cache is tolerated
		// by the subscriber (it logs and returns nil) so a
		// misconfigured dev box never spawns a goroutine that
		// loops forever doing nothing. Mirrors the audit
		// subscriber pattern just above.
		if srv.specCache != nil {
			go func() {
				if err := runOpenAPIDocSubscriber(ctx, pool, srv.specCache, log); err != nil && ctx.Err() == nil {
					log.Error("openapi_doc: subscriber exited", "err", err)
				}
			}()
		}
		// Issue #472 / ADR-058: maintain the on-disk mirror of
		// app_trusted_signers at /etc/faas/secrets/trusted-publishers
		// (the dir imaged reads at startup and on every
		// trusted_signer_changed notify). Without this writer, the
		// disk stays empty and every signed deploy fails with
		// ErrSignatureInvalid even when the operator has correctly
		// onboarded publishers via the API. The writer is the only
		// producer of the per-app PEM files; the dir is created
		// at daemon start so the first ever onboard succeeds even
		// before the first notify.
		trustedDir := os.Getenv("FAAS_TRUSTED_PUBLISHERS_DIR")
		if trustedDir != "" && srv.store != nil {
			go func() {
				if err := runTrustedPublisherWriter(ctx, pool, srv.store, trustedDir, log); err != nil && ctx.Err() == nil {
					log.Error("trusted-publisher-writer: exited", "err", err)
				}
			}()
		}
	}
	return runWithDeps(ctx, log, deps)
}

// graceSenderAdapter bridges apid's Mailer (which sends the apid
// Message struct) to pkg/grace.Sender (which takes primitive args).
// Kept inline so the production apid binary doesn't pull the apid
// Message type into pkg/grace — pkg/grace's signature is intentionally
// narrow so it has no apid dependency.
type graceSenderAdapter struct{ m Mailer }

func (g graceSenderAdapter) Send(ctx context.Context, to []string, subject, body string) error {
	return g.m.Send(ctx, Message{To: to, Subject: subject, TextBody: body})
}

// mailAdapter bridges pkg/mail.Sender (the cross-daemon outbound-email
// seam) to apid's internal Mailer interface. Same shape as
// graceSenderAdapter above but in the opposite direction: the apid
// Message type stays free of pkg/mail so daemons that link apid don't
// pull the mail deps transitively. Gap G4 closure: the production
// wire-up in runWithDeps wraps mail.SenderFromEnv(...)
// (Resend/Postmark/Log/Noop) in this adapter so magic-link + dunning +
// quota-warning + deletion-pending emails actually reach the customer.
type mailAdapter struct{ s mail.Sender }

// newMailerAdapter wraps a pkg/mail.Sender so it satisfies apid's
// Mailer interface. Returns noopMailer{} for a nil sender so callers
// never need to nil-check (matches newServerWithDeps's nil → noop
// convention).
func newMailerAdapter(s mail.Sender) Mailer {
	if s == nil {
		return noopMailer{}
	}
	return mailAdapter{s: s}
}

func (a mailAdapter) Send(ctx context.Context, m Message) error {
	return a.s.Send(ctx, mail.Message{
		To:        m.To,
		Subject:   m.Subject,
		TextBody:  m.TextBody,
		HTMLBody:  m.HTMLBody,
		Headers:   m.Headers,
		MessageID: m.MessageID,
	})
}

// mailStoreCheckerAdapter narrows state.Store to the
// mail.SuppressionChecker surface SuppressingSender needs. Keeps
// pkg/mail free of an import on pkg/state (the leaf-package
// interface seam at pkg/mail/suppression.go is the rule), and lets
// the apid / meterd wire-up pick the same Store the rest of the
// daemon uses without dragging the full state.Store surface into
// the decorator. PR #1191 fixup.
type mailStoreCheckerAdapter struct{ s state.Store }

func (a mailStoreCheckerAdapter) IsMailSuppressed(ctx context.Context, email string) (bool, error) {
	return a.s.IsMailSuppressed(ctx, email)
}

// graceIntervalFromEnv reads FAAS_GRACE_INTERVAL to let the e2e test
// accelerate the sweep (default 60s is correct for production; a CI
// test sets it to a few hundred ms so the 30-day "grace expired"
// case runs in seconds, not minutes). Returns 0 to let pkg/grace
// fall back to its 60s default.
func graceIntervalFromEnv(log *slog.Logger) time.Duration {
	v := os.Getenv("FAAS_GRACE_INTERVAL")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		if log != nil {
			log.Warn("FAAS_GRACE_INTERVAL unparseable, using default",
				"value", v, "err", err)
		}
		return 0
	}
	return d
}

// stuckAfterFromEnv reads FAAS_SAFEDEPLOY_STUCK_AFTER to let
// operators tune the RecoverRollout stuck-detection window per
// environment (production-leveling Stream C). Default 30 min
// (pkg/state.RecoverRolloutStuckAfter) is the ADR-122 canned
// value; a dev cluster can drop it to 5 min so e2e tests don't
// wait half an hour for "this rollout is stuck" to fire. Returns
// 0 on unset / unparseable so the setter silently ignores it
// and the canned value stays in effect.
func stuckAfterFromEnv(log *slog.Logger) time.Duration {
	v := os.Getenv("FAAS_SAFEDEPLOY_STUCK_AFTER")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		if log != nil {
			log.Warn("FAAS_SAFEDEPLOY_STUCK_AFTER unparseable, using default",
				"value", v, "err", err)
		}
		return 0
	}
	return d
}

// dpaPathFromEnv resolves the DPA template path. Production wires an
// explicit FAAS_DPA_PATH pointing at the installed /etc/faas/dpa.md;
// when that's unset, fall back to <cwd>/docs/DPA.md if that file
// exists, so `go run ./cmd/apid` from the repo root serves the dev
// template without a setup step. When neither is set the handler
// returns 503 — a misconfigured production deploy is observable
// rather than silently empty (see handlers_account.go::dpaTemplate).
func dpaPathFromEnv(getenv func(string) string) string {
	if p := getenv("FAAS_DPA_PATH"); p != "" {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cwd, "docs", "DPA.md")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	ops := wire.NewOpsMetrics("apid")
	traceShutdown, traceErr := trace.InitTracerWithRegistry(ctx, "apid", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	if traceErr != nil {
		return fmt.Errorf("apid: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("apid: trace shutdown failed", "err", err)
		}
	}()

	// ADR-094: pool-close gate. closePool closes the pool; the
	// Once guards against double-close when an early-return path
	// closes the pool explicitly and the deferred fallback also
	// runs. Production wires deps.closePool to the helper built
	// in run() right after db.Open; tests that pass a no-op
	// closure (defaultDeps.zeroClosePool) keep the existing
	// behaviour. The Once-fallback ensures any pre-bind path
	// that forgets to call closePoolOnce explicitly still closes
	// the pool on return — defence-in-depth on top of the
	// explicit calls below.
	closePool := deps.closePool
	closePoolOnce := sync.OnceFunc(func() {
		if closePool != nil {
			closePool()
		}
	})
	defer closePoolOnce()

	// PR-0 (issue #678): cfg carries the issue-#678 surface
	// (Load*TLS, Get*) into every helper that used to read FAAS_APID_*
	// inline. deps.config is set by run() after LoadConfig returns;
	// the test seam can set it directly via runDeps.config. When
	// nil (the pre-PR-#678 test path that calls runWithDeps
	// directly without threading a Config), fall back to a
	// defaults-only Config — the helpers tolerate nil-receiver,
	// but the cfg.Get* calls don't, so an empty Config is the
	// safe shape.
	cfg := deps.config
	if cfg == nil {
		cfg = &Config{}
	}

	// PR-B (issue #678 / ADR-056): handshake-layer NodeVerifier. The
	// verifier sits in front of every mTLS leg on this daemon
	// (githubd client dial, advisory server listener, githubd-bridge
	// server listener) so leaf-CNs from peers not in the registered
	// compute_nodes set are rejected before stdlib trust returns
	// success. Stdlib chain/SAN/EKU still runs first — the verifier
	// augments (never replaces) the stdlib trust path.
	//
	// Single-box apid (cfg.NodeName == "") does NOT construct a
	// verifier — stdlib trust alone runs. Multi-box apid constructs
	// a PG-backed verifier, refreshes once at startup (so the first
	// handshake after listen sees a populated snapshot), and pumps
	// compute_node_changed notifications into a drain goroutine for
	// the lifetime of the daemon.
	//
	// Last-known-good posture: PGNodeVerifier.Refresh keeps the
	// previous snapshot on loader failure (per pkg/wire/pgverifier.go
	// the snapshot is locked behind an RWMutex and Refresh only
	// swaps on success). A de-sync to "allow nothing" would brick
	// the cluster's mTLS legs (every handshake would fail), so the
	// contract is "best effort refresh on every notify; never brick".
	//
	// The preLoadedNodeVerifier test seam lets tests inject a stub
	// wire.NodeVerifier without booting Postgres. When both deps.pool
	// is nil (test shape that builds runDeps directly) and
	// preLoadedNodeVerifier is nil, the block short-circuits to nil —
	// identical to the pre-PR-B single-box behaviour. The dial sites
	// consume the wire.NodeVerifier interface, so production
	// (*wire.PGNodeVerifier) and test stubs (a hand-rolled NodeVerifier)
	// flow through the same Load*TLSWithVerifier(nodeVerifier) call
	// sites.
	var nodeVerifier wire.NodeVerifier
	if deps.preLoadedNodeVerifier != nil {
		// Test seam: use the stub verifier directly. Construction
		// is intentionally lazy — Refresh is NOT called because the
		// stub doesn't carry a snapshot; tests assert the verifier
		// reaches the dial sites, not the Refresh contract.
		nodeVerifier = deps.preLoadedNodeVerifier
	} else if cfg.NodeName != "" && deps.pool != nil {
		pgv := wire.NewPGNodeVerifier(wire.NewPGNodeLoader(deps.pool), log)
		// Drive a synchronous startup Refresh so the first
		// handshake after listen sees a populated snapshot. The
		// existing defer closePoolOnce() at the top of runWithDeps
		// closes the pool on this early-return — matches the
		// schedd/vmmd pattern (cmd/schedd/main.go:295-297,
		// cmd/vmmd/main.go:839-841).
		if _, err := pgv.Refresh(ctx); err != nil {
			return fmt.Errorf("apid: node verifier startup refresh: %w", err)
		}
		go func() {
			ch, err := db.SubscribeWithReconnect(ctx, deps.pool, []string{db.NotifyComputeNodeChanged}, log)
			if err != nil {
				log.Error("apid: node verifier LISTEN failed", "err", err)
				return
			}
			if rerr := pgv.Run(ctx, ch); rerr != nil && !errors.Is(rerr, context.Canceled) {
				log.Error("apid: node verifier exited", "err", rerr)
			}
		}()
		nodeVerifier = pgv
	}

	store := deps.store()

	// Dev-only: seed a Free account bound to $FAAS_DEV_TOKEN so the CLI can be
	// exercised end-to-end without the (browser-paste) signup flow. Never set in
	// production — the Postgres store + real login supersede this.
	if tok := deps.getenv("FAAS_DEV_TOKEN"); tok != "" {
		if err := seedDevAccount(ctx, store, tok); err != nil {
			return err
		}
		log.Warn("dev account seeded from FAAS_DEV_TOKEN — do not use in production")
	}

	// M7: pass the Stripe webhook secret (env-loaded) and the mailer
	// (log-only until gap G4 is closed). Empty secret = dev mode (the
	// webhook accepts unsigned payloads; never deploy this way).
	stripeSecret := deps.getenv("STRIPE_WEBHOOK_SECRET")
	// Issue #246 acceptance item 8: Resend webhook signing secret.
	// Empty fails-closed (the handler returns 503). Operators on
	// a box that does not need Resend integration can leave it
	// unset — the route is mounted unconditionally and the
	// fail-closed 503 is the safe default.
	resendSecret := deps.getenv("FAAS_MAIL_RESEND_WEBHOOK_SECRET")
	// Gap G4 closure (ADR-115): wire the env-driven mail factory so
	// prod boots with FAAS_MAIL_TRANSPORT=resend and emails go out
	// for real. Tests + dev can keep mailer nil and the factory
	// returns a log sender — behaviour matches the pre-PR
	// newLogMailer(log) wiring. Operator-selected resend / postmark
	// with the credential env var empty is fail-closed (ADR-115
	// §D5); the wrapped ErrMailerMisconfigured propagates here so
	// the daemon refuses to boot instead of silently dropping
	// email into slog.
	//
	// Issue #246 extends that contract from "credential missing" to
	// "transport unselected": on a non-dev box, an unset or unknown
	// FAAS_MAIL_TRANSPORT also fails closed via ErrMailUnsetInProd
	// / ErrMailUnknownTransport. Operators who really do want mail
	// in the journal can set FAAS_MAIL_TRANSPORT=log; developers
	// iterating locally can set FAAS_DEV=1 to fall back to log when
	// the transport is unset. Both escapes are documented in the
	// boot hint below so the message names every escape hatch.
	m := deps.mailer
	if m == nil {
		var err error
		m, err = mail.SenderFromEnv(deps.getenv, log)
		if err != nil {
			return fmt.Errorf("apid: %w\n"+
				"  fix one of:\n"+
				"    - set FAAS_MAIL_TRANSPORT=resend (or postmark) plus FAAS_MAIL_FROM and the provider key in /etc/faas/sealed.env\n"+
				"    - set FAAS_MAIL_TRANSPORT=log to keep mail in the journal\n"+
				"    - set FAAS_DEV=1 on a dev/CI box where unset transport should resolve to log", err)
		}
	}
	// PR #1191 fixup: wrap the transport with the decorator stack so
	// every send path is suppressed-aware + 429/5xx-retried. Without
	// this the bounce handler's suppression rows never gate outbound
	// mail and a transient Resend failure leaks back to the HTTP
	// caller instead of being retried within the wall-clock budget.
	//
	// SuppressingSender is outermost — a suppressed address costs
	// zero HTTP attempts. RetryingSender wraps the transport so the
	// decorator chain matches the plan:
	//
	//   SuppressingSender
	//     └── RetryingSender
	//           └── SenderFromEnv result
	//
	// The store is the state.Store that backs IsMailSuppressed —
	// the same store the rest of apid holds. Tests inject a
	// dependency that bypasses this block (deps.mailer is
	// non-nil), so the unit-test surface never sees the stack.
	if deps.mailer == nil && m != nil && deps.store != nil {
		transportLabel := strings.ToLower(deps.getenv("FAAS_MAIL_TRANSPORT"))
		m = &mail.SuppressingSender{
			Inner: &mail.RetryingSender{
				Inner:         m,
				TransportName: transportLabel,
				Log:           log,
			},
			Store: mailStoreCheckerAdapter{s: deps.store()},
			Log:   log,
		}
	}
	mailer := newMailerAdapter(m)
	// M7.5: githubd socket path (ADR-012). Empty = stub client (every
	// method returns api.Problem{Code:"githubd_not_ready"}), which is
	// fine until githubd is actually deployed on this host.
	//
	// ADR-052: multi-box deployments dial githubd over tcp:// +
	// mTLS. cfg.LoadGithubdTLS reads the githubd_tls_* cluster
	// from apid.toml (issue #678 PR-0); the env-var analogue
	// FAAS_GITHUBD_TLS_* was the pre-PR-#678 path. Empty TLS
	// cluster returns (nil, nil) and the unix path keeps working.
	// PR-0 is behaviour-preserving — see cmd/apid/config.go for
	// the env-overlay contract.
	//
	// PR-B (issue #678 / ADR-056): the WithVerifier variant threads
	// the handshake-layer NodeVerifier through LoadClientTLSConfig
	// → SetVerifyPeerCertificate → crypto/tls. cfg.NodeName == ""
	// (single-box dev) leaves nodeVerifier nil; the wire helper's
	// setVerifyHook no-ops on a nil verifier and the stdlib trust
	// path runs unchanged.
	//
	// ADR-052 §5 / PR-E: route the load through the WithReload
	// factory so a SIGHUP-driven reload swaps the client leaf on
	// the next outbound githubd handshake. githubdRotator holds
	// the live *tls.Config; newGithubdClient captures the rotator's
	// initial material at construction but stdlib's
	// GetClientCertificate callback consults the rotator at every
	// handshake.
	githubdRotator := wire.NewTLSRotator(nil)
	githubdTLS, err := cfg.LoadGithubdTLSWithPrefixAndVerifierAndReload(nodeVerifier, githubdRotator.Reload(nil))
	if err != nil {
		return fmt.Errorf("apid: githubd TLS: %w", err)
	}
	githubdRotator.Set(githubdTLS)
	if deps.captureDialTLS != nil {
		deps.captureDialTLS("githubd", githubdTLS)
	}
	githubd := newGithubdClient(ctx, cfg.GetGithubdSocket(deps.getenv), githubdTLS, log)
	// M7.5: dashboard session manager. Loads the 32-byte key from
	// FAAS_SESSION_KEY (hex-encoded); empty in dev = ephemeral key +
	// warning so the daemon still boots for local testing. Production
	// MUST set this to the contents of /etc/faas/secrets/session.key
	// (root:root 0400, spec §11).
	//
	// Issue #585 / ADR-127 review-fix (PR #1078 follow-on): the
	// loader returns (nil, sentinel) when the key is malformed,
	// unreadable, or wrong-byte-length. We refuse to boot in that
	// case — a nil session.Manager reaches sessionAuth which calls
	// s.sessions.Verify and crashes on the first authenticated
	// dashboard request (silent nil-deref). The pre-PR-1078 code
	// logged the sentinel as a dev-mode warning and continued,
	// which is the A5 silent-degradation class the apid loader was
	// specifically written to close. Empty env (the dev fallback)
	// still returns a real manager + a warning string so local
	// iteration stays unblocked.
	sessions, sessionsWarn := loadSessionManager(deps.getenv, log)
	if sessions == nil {
		return fmt.Errorf("apid: session manager: %s", sessionsWarn)
	}
	if sessionsWarn != "" {
		log.Warn("session manager in dev mode; sessions reset on restart", "warning", sessionsWarn)
	}
	// Issue #419 / ADR-046: validate the sign-in OAuth env vars at
	// boot. Half-configured (e.g. GOOGLE_CLIENT_ID set but
	// GOOGLE_CLIENT_SECRET unset) refuses to start — that's the
	// 500-into-customer-request footgun the loader exists to close.
	// Both-unset is permitted: the operator chose not to ship OAuth
	// on this host, the handlers return 503 oauth_provider_unavailable,
	// and the dashboard's login template hides the buttons. The
	// resolved config rides on *server via WithOAuthConfig so the
	// handlers, /v1/auth/capabilities, and renderLoginForm share one
	// source of truth (no os.Getenv at request time).
	oauthCfg, err := auth.LoadSignInConfigFromEnv(deps.getenv)
	if err != nil {
		return fmt.Errorf("apid OAuth configuration: %w", err)
	}
	if !oauthCfg.Google.Enabled() && !oauthCfg.GitHub.Enabled() {
		log.Warn("OAuth disabled on this host — both providers unset; /v1/auth/{google,github} return 503 oauth_provider_unavailable, /login hides the OAuth buttons",
			"google_enabled", oauthCfg.Google.Enabled(),
			"github_enabled", oauthCfg.GitHub.Enabled())
	} else {
		log.Info("OAuth sign-in capability",
			"google_enabled", oauthCfg.Google.Enabled(),
			"github_enabled", oauthCfg.GitHub.Enabled())
	}
	srv := newServerWithDeps(store, log, cfg.GetAppsDomain(deps.getenv), deps.notif(), stripeSecret, mailer, githubd, sessions, nil, deps.loginTTL, dpaPathFromEnv(deps.getenv)).
		WithCLIAuthURLBase(cfg.GetCLIAuthURLBase(deps.getenv))
	objectRegistry, err := objectstorage.Load(deps.getenv)
	if err != nil {
		return fmt.Errorf("apid object storage configuration: %w", err)
	}
	srv.WithObjectStorage(objectRegistry)
	srv.WithResendWebhookSecret(resendSecret)
	// Issue #246 acceptance item 8: wire the meterd-owned bounce
	// handler so Resend bounce / complaint events feed the
	// suppression + dunning pipeline. The local-store adapter
	// runs the bounce handler in-process (apid already has the
	// store + audit auditor wired). A future PR can swap this
	// for an RPC adapter once meterd is split onto a separate
	// node and the pipeline ships over pg_notify.
	srv.WithMailBounce(meter.NewLocalBounceHandler(store, srv.audit, log))
	// ADR-132: seed the hot runtime configuration snapshot from the
	// deployment environment, then reconcile durable operator overrides
	// before any listener is exposed. Database state wins over the
	// bootstrap fallback for catalogued runtime settings.
	srv.WithRuntimeConfigManager(newRuntimeConfigManager(deps.getenv))
	// Seed legacy consumers from the same bootstrap snapshot before the
	// durable reconciliation. The durable operator value must win after a
	// restart; calling WithDataPlacement after reconcile would overwrite it
	// with the environment fallback on every boot.
	srv.WithDataPlacement(dataPlacementEnabledFromEnv(deps.getenv))
	if err := srv.runtimeConfig.reconcile(ctx, store); err != nil {
		return fmt.Errorf("apid: reconcile runtime config: %w", err)
	}
	srv.WithOAuthConfig(oauthCfg)

	// PR #1099 P2 redesign: force-park + force-cold-boot now route
	// through the operator_intents table + pg_notify (migrations/00431,
	// pkg/sched/operator_intent_subscriber.go). apid never imports
	// pkg/scheddgrpc — the apid-control-plane-only depguard rule
	// (.golangci.yml:41-58) is preserved. schedd is still the only
	// writer to instances; the trigger is now a Postgres row
	// INSERT, not a direct gRPC call.

	// Issue #142: Stripe billing portal URL template for the changePlan
	// 402 response. Empty = 402 omits billing_portal_url; the dashboard
	// renders a generic "use the billing portal" message. Production sets
	// FAAS_BILLING_PORTAL_URL to a template containing `{account_id}`
	// (replaced at write time) so the customer lands on a Stripe-hosted
	// portal pre-bound to their account.
	//
	// SECURITY: this value is operator-controlled and rendered verbatim
	// into every blocked-upgrade response. A misconfigured value that
	// points at an attacker-controlled host (e.g. an env-var typo or a
	// wrong deploy) misroutes every blocked upgrade. Set it to the
	// operator-hosted Stripe billing portal URL, validate it before
	// deploy, and never interpolate untrusted input.
	srv.WithBillingPortalURL(deps.getenv("FAAS_BILLING_PORTAL_URL"))

	// Billing provider dispatch (ADR-025 / public-release billing).
	// FAAS_BILLING_PROVIDER defaults to "polar" when unset — the loader
	// constructs a *polar.Provider and requires the configured catalog
	// and meter to pass preflight before the daemon accepts billing traffic.
	// The legacy "stripe" opt-in still returns
	// (nil, "stripe", nil) so the changePlan 402 path falls back to
	// the FAAS_BILLING_PORTAL_URL template above (the pre-PR-#3
	// behaviour stays bit-for-bit unchanged).
	//
	// PR-P2: read the [billing] block from apid.toml first, then
	// overlay env on top. Missing file is non-fatal (defaults). Bad
	// TOML is fatal. The loader sees the merged cfg + the raw env
	// reader (closures re-read the secrets the overlay just wrote).
	loadBillingConfig := deps.loadBillingConfig
	if loadBillingConfig == nil {
		loadBillingConfig = billingloader.LoadBillingConfigFromPath
	}
	billingCfg, err := loadBillingConfig(deps.configPath)
	if err != nil {
		return fmt.Errorf("apid: load billing config: %w", err)
	}
	billingCfg = billingloader.ApplyBillingEnvOverlay(billingCfg, deps.getenv)
	billingProv, provName, err := billingloader.LoadProviderForAPID(ctx, billingCfg, deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load billing provider: %w", err)
	}
	if billingProv != nil {
		srv.WithBillingProvider(billingProv)
	}
	log.Info("billing provider loaded", "provider", provName)

	// Issue #299 / ADR-038 Phase 3: SBOM root directory. imagd's syft
	// populator writes CycloneDX JSON to <root>/sboms/<buildID>.cdx.json
	// and stores the relative path in build_provenance.sbom_storage_key.
	// apid joins the relative path against this root at GET
	// /v1/builds/{id}/sbom time. Default is the single-box deploy root
	// (/srv/fc, FAAS_STORAGE_ROOT for the local storage backend); on a
	// remote-storage deploy the operator sets FAAS_SBOM_ROOT to the
	// mirror mount. Empty disables the route — the handler returns 503
	// build_sbom_unavailable (issue #299: "may exist later, retry") so
	// the CLI/SDK can distinguish from 404 "no such build".
	srv.WithSBOMRoot(deps.getenv("FAAS_SBOM_ROOT"))

	// Issue #98 / ADR-028: admin allowlist for /v1/compute-nodes.
	// Empty in dev = all admin routes 403 with code admin_required;
	// production sets FAAS_ADMIN_EMAILS to the operator team's
	// comma-separated addresses. The allowlist is read at startup,
	// so a config change requires a restart — acceptable for the
	// tiny operator surface that exists today.
	srv.WithAdminAllowlist(deps.getenv("FAAS_ADMIN_EMAILS"))

	// Prometheus registry + ops observer middleware (this PR).
	// Built unconditionally so /metrics works even with FAAS_APID_METRICS_ADDR
	// unset (the daemon stays up; only the listener is skipped below).
	wire.BootStamps(ctx, "apid", ops)
	wire.RegisterDefaultOps(ops)
	srv.WithOpsMetrics(ctx, ops)

	// ADR-093 / PR-D: end-to-end request budgets on the apid
	// listener. Same shape as gatewayd-public PR-B: a fresh
	// prometheus registry holds the budget histogram + counter,
	// the middleware stamps a per-request Budget onto r.Context(),
	// and the /metrics handler combines ops + budget gatherers
	// into a single scrape. Default is api.RequestBudgetApidDefault
	// (5 s); Max is api.RequestBudgetMax (30 s); apid handlers
	// that have an existing per-call WithTimeout (PromQL 3 s,
	// billingOps 30 s, sync-invoke 5 s / 30 s) tighten themselves
	// against the budget via reqbudget.WithCeiling (PR-D §3).
	budgetReg := prometheus.NewRegistry()
	budgetMetrics, err := reqbudget.NewMetrics(budgetReg, "apid")
	if err != nil {
		return fmt.Errorf("apid: reqbudget metrics: %w", err)
	}
	budgetCfg, err := reqbudget.NewMiddlewareConfig(reqbudget.MiddlewareConfig{
		Default: api.RequestBudgetApidDefault,
		Max:     api.RequestBudgetMax,
		Route:   "admin",
		Metrics: budgetMetrics,
		Log:     log,
		DefaultFor: func(r *http.Request) time.Duration {
			if reqbudget.IsSyncInvokeRequest(r) {
				// invokeApp applies the plan-specific 5s/30s wait;
				// give that handler the full platform ceiling so the
				// outer apid budget does not truncate paid plans.
				return api.RequestBudgetMax
			}
			return 0
		},
	})
	if err != nil {
		return fmt.Errorf("apid: reqbudget middleware config: %w", err)
	}

	// issue #517 / PR-C / ADR-064: thread the events Platform
	// into the server so the audit subscriber (which receives
	// the signature-rejection kinds from imaged's verify hook)
	// can emit the typed wake.deploy_failed row. nil opts out
	// (the unit tests don't wire it). The Platform writes the
	// events row + bumps wake_phase_emitted_total.
	eventsPlatform := events.NewPlatform("apid", store, log, ops, nil)
	srv.WithEventsPlatform(eventsPlatform)

	// ADR-093: gatewayd-internal control-listener URL for the
	// per-route observability reader. Default
	// http://127.0.0.1:9090 matches gatewayd-internal's default
	// control bind (cmd/gatewayd-internal/config.go ControlAddr,
	// default 127.0.0.1:9090). Production overrides via
	// FAAS_GATEWAYD_CONTROL_URL when the daemons are split across
	// nodes; same-box is the only supported posture today
	// (cross-box will need an mTLS-terminating reverse-proxy —
	// out of scope for this PR). Empty string disables the
	// /v1/apps/{slug}/routes surface; getAppRoutes renders the
	// unavailable state so the dashboard can distinguish
	// "operator hasn't wired it" from "no traffic yet". The
	// unresolved env value is what the test seam surfaces
	// (TestAppRoutes_MissingURLRendersUnavailable pins the empty
	// contract); main() always applies the default at boot so a
	// same-box install doesn't brick the per-route surface just
	// because the operator never exported the env var.
	srv.WithGatewaydControlURL(resolveGatewaydControlURL(deps.getenv))

	// ADR-126 / issue #975 item #2: the in-process LRU backing
	// the `?source=auto` OpenAPI generation. Constructed once
	// at boot (default cap 256, TTL 5 min — both constants
	// live in pkg/openapidiff/spec_cache.go). The subscriber
	// (runOpenAPIDocSubscriber, started in bgBefore above)
	// flushes per-app on either pg_notify channel.
	srv.WithSpecCache(openapidiff.NewSpecCache())

	// Status page (spec §12 public surface). The Prometheus URL is
	// the local box's Prometheus installed by deploy/ansible/roles/
	// prometheus (default http://127.0.0.1:9095 on the control plane). The HTML path defaults
	// to /etc/faas/statuspage/index.html; a dev override
	// (FAAS_STATUSPAGE_PATH) lets us point at deploy/statuspage/
	// index.html without installing.
	srv.WithStatusCache(
		resolvePrometheusURL(deps.getenv, cfg.Role),
		deps.getenv("FAAS_STATUSPAGE_PATH"),
	)

	// G2: load the host age recipient so the secrets PUT handler can seal.
	// vmmd owns the private half; we only need the public recipient string.
	// The recipient path is opt-in via FAAS_HOST_AGE_RECIPIENT_PATH — set in
	// production (and by the e2e harness) to the file vmmd writes
	// (/etc/faas/secrets/host.age.pub by default). When the env var is unset,
	// the var stays nil and PUT /secrets returns 503 — a loud, observable
	// signal that the box is misconfigured rather than a silent accept-and-
	// drop of plaintext. The unit tests don't set the var because the
	// handlers they're checking don't exercise the seal path.
	if recipientPath := deps.getenv("FAAS_HOST_AGE_RECIPIENT_PATH"); recipientPath != "" {
		r, err := secretbox.LoadRecipient(recipientPath)
		if err != nil {
			return fmt.Errorf("apid: load host age recipient %q: %w", recipientPath, err)
		}
		setSecretRecipient = func() *age.X25519Recipient { return r }
		// Issue #463 / ADR-068: the sidecar seal helper reuses the
		// same host age recipient (one age identity per host). A
		// separate getter keeps the seal helpers testable in
		// isolation without leaking the secret-handler test seam.
		setSidecarRecipient = func() *age.X25519Recipient { return r }
		log.Info("host age recipient loaded", "path", recipientPath)
	} else {
		log.Warn("FAAS_HOST_AGE_RECIPIENT_PATH unset — secrets PUT will return 503")
	}

	// MFA (IAM-2, issue #186): load the host age identity so
	// /v1/account/mfa/confirm and /verify can unseal the TOTP
	// secret. Same key file vmmd reads on wake. The identity
	// stays in-process; we never log it or write it to disk.
	// FAAS_HOST_AGE_IDENTITY_PATH is required only when MFA is
	// in use — without it, /enroll still works (recipient-only)
	// but /confirm /verify /disable /recover all 503.
	//
	// Issue #316 / ADR-057: we also load host.age.previous via
	// LoadHostKeys(dir) and wire the slice through SetMFAIdentities
	// so the 30-day rotation overlap window unseals envelopes
	// sealed under the previous key. The single-identity SetMFAIdentity
	// stays wired for backward compat with the existing tests.
	if identityPath := deps.getenv("FAAS_HOST_AGE_IDENTITY_PATH"); identityPath != "" {
		ident, err := secretbox.LoadHostKey(identityPath)
		if err != nil {
			return fmt.Errorf("apid: load host age identity %q: %w", identityPath, err)
		}
		SetMFARecipient(func() *age.X25519Recipient { return ident.Recipient() })
		SetMFAIdentity(func() *age.X25519Identity { return ident })
		log.Info("host age identity loaded for MFA", "path", identityPath)

		// Rotation-overlap wiring: load the multi-identity slice from
		// the same directory. If LoadHostKeys fails (e.g. .previous
		// mode tripwire fired mid-deploy) we keep the single-identity
		// path wired and log a Warn — the box is still unsealing
		// envelopes under the current key, just not the previous one.
		// A hard error would lock every MFA customer out, which is
		// worse than the operator-visible degraded-mode log line.
		identities, loadErr := secretbox.LoadHostKeys(filepath.Dir(identityPath))
		if loadErr != nil {
			log.Warn("apid: LoadHostKeys (rotation overlap) failed; MFA unseal will work only for envelopes sealed under the current host.age",
				"dir", filepath.Dir(identityPath), "err", loadErr.Error())
		} else {
			SetMFAIdentities(func() []*age.X25519Identity { return identities })
			if len(identities) > 1 {
				log.Info("apid: rotation overlap active — MFA unseal falls back across current + previous host.age",
					"current", identities[0].Recipient().String(),
					"previous", identities[1].Recipient().String())
			}
		}
	} else {
		log.Warn("FAAS_HOST_AGE_IDENTITY_PATH unset — MFA /confirm, /verify, /disable, /recover will return 503")
	}

	// ADR-117 PR-C: load the host HMAC key so sealAndPersist
	// can compute secretbox.ValueFingerprint(plaintext,
	// hostHMACKey) BEFORE SealOne. The hash is the
	// value-equality discriminator the env-diff endpoint reads
	// (GET /v1/apps/{slug}/env-diff). 32 bytes, mode 0o400 OR
	// 0o440 (the systemd-credentials posture). Refusing to start
	// on missing/permissive/length-mismatch matches the posture
	// of the age recipient + identity loaders above — a
	// misconfigured box must NOT silently write a NULL value_hash
	// column.
	if hmacKeyPath := deps.getenv("FAAS_HOST_HMAC_KEY_PATH"); hmacKeyPath != "" {
		k, err := loadHostHMACKey(hmacKeyPath)
		if err != nil {
			return fmt.Errorf("apid: load host HMAC key %q: %w", hmacKeyPath, err)
		}
		hostHMACKey = func() []byte { return k }
		log.Info("host HMAC key loaded for value_hash", "path", hmacKeyPath)
	} else {
		// ADR-117 D2 + D9 posture: refuse to start without the
		// per-host HMAC key. The legacy "503 on PUT" path is the
		// fallback for the recipient (sealed-envelope work), but
		// here the discriminator is a metadata field on every row
		// and a silent NULL would survive indefinitely (lazy
		// backfill on rotate). A startup refusal is the right
		// signal.
		return fmt.Errorf("apid: FAAS_HOST_HMAC_KEY_PATH unset — refusing to start without the per-host HMAC key (ADR-117 D2 + D9; bootstrap with `openssl rand -out /etc/faas/secrets/host.hmac.key 32 && chmod 0400 /etc/faas/secrets/host.hmac.key`)")
	}

	// Issue #286 / CodeQL alert #121: load (or generate + persist)
	// a per-box audit HMAC key and wire it into pkg/auth so
	// HashEmail uses HMAC-SHA256 instead of plain SHA-256. The
	// plain SHA-256 form is rainbow-table-reversible — a leaked
	// `events.data` column lets an adversary precompute hashes for
	// common emails and reverse the column. HMAC keyed by a
	// per-box secret closes that path while preserving the audit-
	// row join-key contract (deterministic for a given (email,
	// secret) pair). The key is held in-process only — never
	// written to events, never logged, never returned from any
	// HTTP handler.
	//
	// Loading precedence:
	//   1. FAAS_AUDIT_HMAC_KEY env var (hex-encoded 32 bytes) — the
	//      operator-supplied path; production uses this so the key
	//      survives container restarts via the env-var mount
	//      (Kubernetes secret, systemd EnvironmentFile=).
	//   2. /var/lib/faas/audit-hmac.key (0o600) — the auto-generated
	//      fallback. Generated once on first boot, persisted with
	//      0o600 perms so it survives daemon restart without
	//      requiring operator action. The file path is gated on
	//      FAAS_AUDIT_HMAC_KEY_FILE for tests / non-standard
	//      deployments.
	//   3. nil (zero-key fallback) — only if both above paths fail.
	//      pkg/auth logs a Warn; HashEmail still produces a stable
	//      join key, just without rainbow-table resistance.
	auditHMACKey, err := loadOrGenerateAuditHMACKey(deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load or generate audit HMAC key: %w", err)
	}
	auth.SetHMACSecret(auditHMACKey, log)

	// Recovery-code HMAC key — closes the SHA-256 rainbow-reversal
	// threat on a leaked PG blob (logical change 7 of the IAM-hardening
	// mega-PR, ADR-035 §"Rejected alternatives" #4). The recovery-code
	// hash column is bytea; the digest algorithm is HMAC-SHA256 keyed
	// by a per-box secret. The audit-hmac.key path above uses a
	// Warn-and-continue zero-key fallback because HashEmail joins
	// can degrade gracefully to "this row's join key is rainbow-
	// reversible" — there is no service-level outage. The recovery-
	// code path is different: the recovery code IS the only fallback
	// when the customer's TOTP device is lost. A zero-key HMAC gives
	// exactly the same threat surface as bare SHA-256, so refusing
	// to start is the correct tradeoff — "no service" beats "no
	// defence" for the recovery fall-back path.
	//
	// Loading precedence (mirrors the audit-HMAC loader, with one
	// difference: there is no precedence 3 zero-key fallback):
	//   1. FAAS_MFA_RECOVERY_HMAC_KEY env var (hex-encoded 32 bytes).
	//   2. /var/lib/faas/recovery-hmac.key (0o600) — auto-generated
	//      on first boot, persists across restarts so newly-stamped
	//      rows remain stable across boots (otherwise every restart
	//      would rotate the key and break the entire MFA cohort's
	//      recovery-code store — every customer would have to re-enroll).
	//      The path is gated on FAAS_RECOVERY_HMAC_KEY_FILE for tests
	//      and non-standard state dirs.
	//   3. FAIL — boot refuses. The audit-hmac.key loader returns nil
	//      when no key is configured; this loader returns an error
	//      instead. The error propagates as a top-level run() error and
	//      systemd brings apid back into the failed-state pattern.
	recoveryHMACKey, err := loadOrGenerateRecoveryHMACKey(deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load or generate recovery HMAC key: %w", err)
	}
	if err := authcode.SetHMACSecret(recoveryHMACKey); err != nil {
		// defence-in-depth: SetHMACSecret returns ErrNoHMACKey if the
		// loader above somehow handed back an empty slice (e.g. a
		// future refactor that adds a nil-tolerant branch). The
		// loader is the canonical gate, but the second check here
		// closes a foot-gun class of bugs.
		return fmt.Errorf("apid: set recovery HMAC secret: %w", err)
	}

	// ADR-089 PR-C — background re-seal runner. Opt-in via
	// FAAS_REKEY_ENABLED; default false. When enabled, the
	// runner walks app_secrets and re-seals every row under the
	// current host identity (pkg/rekey.Replayer wrapped by
	// cmd/apid/rekey_runner.go::Runner). The runner is set on
	// the server BEFORE deps.bgBefore runs so the goroutine
	// launch below can call its Run() method without a nil check
	// inside the closure.
	//
	// The runner needs the multi-identity slice loaded earlier
	// (mfaIdentities() — line 800). When the identity path is
	// unset we skip construction (the runner's identities field
	// would be empty); this is consistent with the rest of the
	// identity-dependent surfaces (MFA /confirm, rotate handler)
	// that 503 when no identity is loaded.
	//
	// The audit actor "rekey" is distinct from the apid default
	// "apid" so dashboards filtering on the audit table can
	// separate user-driven rotates (PR-B, actor="apid") from
	// background re-seals (PR-C, actor="rekey"). pkg/audit.New
	// bakes the actor into the Auditor instance at construction
	// (pkg/audit/audit.go:72) — every Emit call carries it.
	if rekeyEnabledFromEnv(deps.getenv) {
		// Mark opted-in BEFORE the runner construction so the HTTP
		// handler returns rekey_no_identities (not the misleading
		// rekey_disabled) if identities are empty. PR #825 follow-up
		// — the operator DID set the flag; the misconfig is the
		// missing identity path.
		srv.MarkRekeyRunnerOptedIn()
		identities := mfaIdentities()
		if len(identities) == 0 {
			log.Warn("FAAS_REKEY_ENABLED=true but no host age identities loaded — background re-seal disabled")
		} else {
			rekeyAudit := audit.New(store, log, nil, "rekey")
			progressPath := envOr(rekeyProgressEnvVar, rekeyProgressPathDefault)
			runner, err := NewRunner(RunnerOpts{
				Store:        store,
				Audit:        rekeyAudit,
				Identities:   identities,
				HostHMACKey:  hostHMACKey(),
				ProgressPath: progressPath,
				Log:          log,
			})
			if err != nil {
				return fmt.Errorf("apid: build rekey runner: %w", err)
			}
			srv.WithRekeyRunner(runner)
			log.Info("background re-seal enabled",
				"progress_path", progressPath,
				"identity_count", len(identities),
			)
		}
	} else {
		log.Info("background re-seal disabled; set FAAS_REKEY_ENABLED=true to opt in (see docs/ops/host-age-rotation.md)")
	}

	// A single reconnecting LISTEN session fans invocation_done events out
	// to all synchronous requests. If the optimization cannot start, keep
	// the legacy per-request listener path so a transient database/listener
	// issue does not prevent apid from serving traffic.
	if deps.pool != nil {
		completion := newInvocationCompletionWaiter(deps.pool, log)
		if err := completion.Start(ctx); err != nil {
			log.Warn("invocation completion fan-out unavailable; using per-request LISTEN fallback", "err", err)
		} else {
			srv.WithInvocationCompletionWaiter(completion)
		}
	}

	// Optional pre-listen hook (DNS poller in production; nil in tests).
	if deps.bgBefore != nil {
		deps.bgBefore(ctx, log, srv)
	}

	listenBind := cfg.GetListenAddr(deps.getenv)
	// ADR-093 / PR-D: wrap srv.handler() with the BudgetMiddleware
	// so every apid request runs under a Budget-decorated ctx.
	// Per-handler WithTimeout ceilings (PromQL 3 s, billingOps 30 s,
	// sync-invoke 5 s / 30 s) become children of the budget via
	// reqbudget.WithCeiling — the budget tightens the cap, never
	// loosens it.
	httpSrv := deps.newSrv(listenBind, budgetCfg.Middleware(srv.handler()), cfg)

	l, err := deps.listen("tcp", listenBind)
	if err != nil {
		return err
	}

	// Optional /metrics listener (this PR). Sits on its own bind
	// address so a port collision can't take the daemon down. Empty
	// FAAS_APID_METRICS_ADDR = no listener (the scrape observer is
	// still wired into the main mux via observeWrap; only the
	// listener is skipped). Mirrors cmd/builderd/main.go:146-157.
	//
	// PR-0 (issue #678): resolveMetricsAddr honours the explicit-
	// empty-disable semantic by reading FAAS_APID_METRICS_ADDR via
	// the test seam (deps.getenv), then falls back to cfg.GetMetricsAddr
	// (TOML), then to metricsAddrDefault.
	//
	// ADR-093 / PR-D: combine ops + budgetReg into a single
	// prometheus.Gatherers so /metrics scrapes both the standard
	// apid_* ops metrics AND the budget histogram + counter
	// families in one round-trip.
	var metricsSrv *http.Server
	if metricsAddr := resolveMetricsAddr(deps.getenv, cfg.GetMetricsAddr(deps.getenv)); metricsAddr != "" {
		// Issue #571 PR-A2: wire /healthz + /readyz on the
		// metrics mux (operator-side, loopback-only) so the LB
		// scrape + on-box monitoring see the same readiness as
		// the customer-side /readyz (cmd/apid/handlers_ready.go)
		// without depending on the cookie-auth path. The probe
		// uses NewPGPingSignal against the same pool the
		// customer-side /readyz pings, so both endpoints track the
		// same source of truth. nil pool (test path with no
		// metrics_addr wired) short-circuits to an always-ready
		// signal so unit tests don't construct a pgxpool they
		// don't need.
		var apidProbe wire.ReadyzProbe
		if deps.pool != nil {
			pgSig, pgStop := wire.NewPGPingSignal(ctx, deps.pool, 5*time.Second)
			apidProbe.RegisterSignal(pgSig, pgStop)
			// NOTE: pgStop is wired through pkg/wire.ReadyzProbe.Drain
			// (issue #571 PR-A2 / Finding 4 PR #1091 review). The
			// earlier `defer pgStop()` is gone — Drain fires the
			// helper goroutine's stopper synchronously before
			// flipping the signal to "draining", so a /readyz
			// scrape that lands during the SIGTERM drain window
			// sees the helper already stopped (no re-flip race).
		} else {
			// Test path: no pool. Always-ready so /readyz returns
			// 200. Mirrors the pre-split degradation pattern.
			s := apidProbe.Register()
			s.Set(true, "")
		}
		apidProbe.SetReadyObserver(func(ready bool, reason string) {
			ops.MarkReady("apid", ready, reason)
		})
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.HandlerFor(
			prometheus.Gatherers{ops.Registry(), budgetReg},
			promhttp.HandlerOpts{Registry: ops.Registry()},
		))
		wire.ControlMuxLite(metricsMux, apidProbe.ReadyFunc(), apidProbe.ReasonFunc())
		metricsSrv = &http.Server{
			Addr:    metricsAddr,
			Handler: metricsMux,
			// Issue #995 Phase 1: tighten the metrics listener.
			// Loopback-only and no body, so the production defaults
			// (60s/300s) are looser than needed — a 10s read / 10s
			// write pair kills stale scrapes fast and bounds a
			// runaway scraper. MaxHeaderBytes keeps a malformed
			// scraper from blowing the header cap. ReadHeaderTimeout
			// is unchanged (10s).
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
		}
		mLis, err := net.Listen("tcp", metricsAddr)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: metrics listen %q: %w", metricsAddr, err)
		}
		go func() {
			log.Info("apid /metrics listening", "addr", metricsAddr)
			if err := metricsSrv.Serve(mLis); err != nil && err != http.ErrServerClosed {
				log.Error("apid /metrics serve", "err", err)
			}
		}()
	}

	// Wave 0 PR-C / ADR-047: stateless-advisory gRPC listener. vmmd
	// dials /run/faas/apid.sock to forward fanotify batches from
	// guest-init. Empty FAAS_APID_ADVISORY_SOCK disables (matches the
	// metricsAddr explicit-empty pattern so the e2e harness can stamp
	// empty and avoid the bind race).
	//
	// ADR-052: when the target is tcp:// or dns:// (multi-box path),
	// the operator must also set advisory_tls_* in apid.toml to a
	// per-daemon leaf. Single-box deployments leave the TLS cluster
	// unset and continue to use the unix socket; cfg.LoadAdvisoryTLS
	// returns (nil, nil) when all three paths are empty.
	//
	// PR-0 (issue #678): cfg.LoadAdvisoryTLS is the issue-#678 surface
	// that replaces the inline env reads. The behaviour is identical
	// (env-overlay path is preserved via the Get helpers).
	//
	// PR-B (issue #678 / ADR-056): the WithVerifier variant installs
	// the handshake-layer NodeVerifier hook on the server-side
	// tls.Config. vmmd dials in here; the hook rejects any peer
	// whose leaf-CN is not in compute_nodes.name.
	//
	// ADR-052 §5 / PR-E: route through the WithReload factory so a
	// SIGHUP-driven reload swaps the server's leaf via stdlib's
	// per-handshake GetConfigForClient callback.
	var advisorySrv *grpc.Server
	var advisoryLis net.Listener
	advisoryRotator := wire.NewTLSRotator(nil)
	if sock := resolveAdvisorySock(deps.getenv, cfg); sock != "" {
		advisoryTLS, tlsErr := cfg.LoadAdvisoryTLSWithPrefixAndVerifierAndReload(nodeVerifier, advisoryRotator.Reload(nil))
		if tlsErr != nil {
			_ = l.Close()
			return fmt.Errorf("apid: advisory TLS: %w", tlsErr)
		}
		advisoryRotator.Set(advisoryTLS)
		if deps.captureDialTLS != nil {
			deps.captureDialTLS("advisory", advisoryTLS)
		}
		advisorySrv, advisoryLis, err = runAdvisoryServer(ctx, sock, advisoryTLS, srv.store, srv.audit, srv.notif, log, srv.ops)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: advisory listen %q: %w", sock, err)
		}
		go func() {
			log.Info("apid advisory listening", "sock", sock)
			if err := advisorySrv.Serve(advisoryLis); err != nil {
				log.Error("apid advisory serve", "err", err)
			}
		}()
	}

	// Issue #432 phase 5: githubd → apid build-enqueue bridge
	// (separate listener from the advisory socket so an operator
	// can disable one without the other). Empty sock disables the
	// listener entirely (matches the advisory listener's pattern).
	// The bridge receiver implementation is in githubd_bridge.go;
	// it implements the githubdpb.GithubdServer interface (only
	// EnqueueBuild is wired; the rest is UnimplementedGithubdServer).
	//
	// PR-0 (issue #678): cfg.LoadGithubdBridgeTLS is the issue-#678
	// surface that replaces the inline env reads.
	//
	// PR-B (issue #678 / ADR-056): the WithVerifier variant installs
	// the handshake-layer NodeVerifier hook on the server-side
	// tls.Config. githubd dials in here; the hook rejects any peer
	// whose leaf-CN is not in compute_nodes.name.
	//
	// ADR-052 §5 / PR-E: route through the WithReload factory for
	// SIGHUP-driven leaf rotation.
	var bridgeSrv *grpc.Server
	var bridgeLis net.Listener
	bridgeRotator := wire.NewTLSRotator(nil)
	if sock := resolveGithubdBridgeSock(deps.getenv, cfg); sock != "" {
		bridgeTLS, tlsErr := cfg.LoadGithubdBridgeTLSWithPrefixAndVerifierAndReload(nodeVerifier, bridgeRotator.Reload(nil))
		if tlsErr != nil {
			_ = l.Close()
			return fmt.Errorf("apid: githubd bridge TLS: %w", tlsErr)
		}
		bridgeRotator.Set(bridgeTLS)
		if deps.captureDialTLS != nil {
			deps.captureDialTLS("bridge", bridgeTLS)
		}
		bridgeSrv, bridgeLis, err = runGithubdBridgeServer(ctx, sock, bridgeTLS, srv.store, srv.notif, log, srv.ops, spoolRoot(), resolveGithubdStagingRoot(deps.getenv))
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: githubd bridge listen %q: %w", sock, err)
		}
		go func() {
			log.Info("apid githubd bridge listening", "sock", sock)
			if err := bridgeSrv.Serve(bridgeLis); err != nil {
				log.Error("apid githubd bridge serve", "err", err)
			}
		}()
	}

	// ADR-096 PR-A: customer-facing automatic error grouping.
	// The IncrementAppError gRPC server lives behind
	// FAAS_APP_ERRORS_ENABLED. PR-A shipped the kill-switch OFF
	// so the schema populated only by hand; PR-B ships the
	// customer-facing surface (handlers + SDK + OpenAPI) and
	// flips the kill-switch to default-on. Set
	// FAAS_APP_ERRORS_ENABLED=false to cleanly disable both
	// the writer (apid gRPC) and the gateway-side recorder.
	var appErrSrv *grpc.Server
	var appErrLis net.Listener
	appErrRotator := wire.NewTLSRotator(nil)
	if deps.getenv("FAAS_APP_ERRORS_ENABLED") != "false" { //nolint:goconst // kill-switch sentinel; the canonical "true" env literal.
		appErrTarget := cfg.GetAppErrorsTarget(deps.getenv)
		appErrTLS, tlsErr := cfg.LoadAppErrorsTLSWithPrefixAndVerifierAndReload(nodeVerifier, appErrRotator.Reload(nil))
		if tlsErr != nil {
			_ = l.Close()
			return fmt.Errorf("apid: app errors TLS: %w", tlsErr)
		}
		appErrRotator.Set(appErrTLS)
		appErrSrv, appErrLis, err = runAppErrorsServer(ctx, appErrTarget, appErrTLS, srv.store, srv.ops, log)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: app errors server: %w", err)
		}
		go func() {
			log.Info("apid app errors server listening", "target", appErrTarget)
			if err := appErrSrv.Serve(appErrLis); err != nil {
				log.Error("apid app errors serve", "err", err)
			}
		}()
		if appErrTLS != nil {
			appErrHupCh := make(chan os.Signal, 1)
			signal.Notify(appErrHupCh, syscall.SIGHUP)
			defer signal.Stop(appErrHupCh)
			appErrReload := func() (*tls.Config, error) {
				return cfg.LoadAppErrorsTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
			}
			go wire.WatchTLSReload(ctx, log, appErrHupCh, appErrRotator, appErrReload)
		}
		go newAppErrorsPurger(srv.store, nil, srv.ops, log, true).Run(ctx)

		// ADR-127 PR-B: gatewayd-internal → apid
		// IncrementRequestTelemetry streaming RPC. Wired behind
		// FAAS_REQUEST_TELEMETRY_ENABLED so the surface is dormant
		// in environments where the data plane isn't shipped yet.
		// Defaults to enabled when the env var is unset; set
		// FAAS_REQUEST_TELEMETRY_ENABLED=false to disable both the
		// writer (apid gRPC) and the gateway-side recorder.
		//
		// ADR-127 PR-D code-review #3: sharedLimiter is the
		// per-account token-bucket pool shared with
		// runSpansWriterServer below. One *peraccount.Limiter
		// instance covers both the PR-B IncrementRequestTelemetry
		// and PR-D WriteSpansSummary paths so a customer's
		// DebugTelemetryRequestsPerMinute cap is enforced
		// against a single bucket pool, not two independent
		// ones (the prior wiring constructed NewLimiter()
		// inside each helper, giving the customer 2x their
		// plan cap). The bucket cap is frozen on first Take
		// (PR-D code-review #2) so whichever path runs first
		// for an account sets the bucket size; subsequent
		// calls match.
		sharedLimiter := peraccount.NewLimiter()
		if deps.getenv("FAAS_REQUEST_TELEMETRY_ENABLED") != "false" {
			rtSrv, rtLis, err := runRequestTelemetryServer(ctx, srv.store, srv.ops, log, sharedLimiter)
			if err != nil {
				_ = l.Close()
				return fmt.Errorf("apid: request telemetry server: %w", err)
			}
			go func() {
				log.Info("apid request telemetry server listening")
				if err := rtSrv.Serve(rtLis); err != nil {
					log.Error("apid request telemetry serve", "err", err)
				}
			}()
		}

		// ADR-127 PR-D: gatewayd-public → apid AuthenticateKey
		// unary RPC. Lives on /run/faas/auth.sock; the OTel spans
		// writer refuses to start without it (fail-closed posture
		// for an unauthenticated OTLP surface). Defaults to enabled
		// when the env var is unset; set FAAS_AUTH_RPC_ENABLED=false
		// to disable — but doing so will break the OTel handler
		// at the gateway, which is the intended safe default.
		if deps.getenv("FAAS_AUTH_RPC_ENABLED") != "false" {
			authSrv, authLis, err := runAuthServer(ctx, srv.store, log)
			if err != nil {
				_ = l.Close()
				return fmt.Errorf("apid: auth server: %w", err)
			}
			go func() {
				log.Info("apid auth server listening")
				if err := authSrv.Serve(authLis); err != nil {
					log.Error("apid auth serve", "err", err)
				}
			}()
		}

		// ADR-127 PR-D: gatewayd-public → apid
		// WriteSpansSummary unary RPC. The gateway's flush
		// loop drains the per-trace accumulator (Stage 3)
		// every 30s and ships each (trace_id, summary_json,
		// account_id) triple to apid's writer. Gated by
		// FAAS_OTEL_SPANS_WRITER_ENABLED (default true);
		// killing it is the fail-closed kill-switch for the
		// OTel writer's write path (the auth + handler can
		// still run; they just stop landing writes).
		if deps.getenv("FAAS_OTEL_SPANS_WRITER_ENABLED") != "false" {
			swSrv, swLis, err := runSpansWriterServer(ctx, srv.store, srv.ops, log, sharedLimiter)
			if err != nil {
				_ = l.Close()
				return fmt.Errorf("apid: otel spans writer server: %w", err)
			}
			go func() {
				log.Info("apid otel spans writer server listening")
				if err := swSrv.Serve(swLis); err != nil {
					log.Error("apid otel spans writer serve", "err", err)
				}
			}()
		}

		// ADR-095 PR-C: preview teardown janitor. Lives in apid
		// (the sole writer to customer-intent tables per CLAUDE.md
		// line 71) and drives preview rows through the
		// closed → stale → torn_down state machine. Emits
		// db.NotifyAppDelete so schedd reaps in-flight instances
		// for tombstoned apps via its existing app_delete
		// subscriber (pkg/sched/app_delete_subscriber.go).
		go newPreviewJanitor(srv.store, srv.notif, srv.ops, log, true).Run(ctx)

		// ADR-052 §5 / PR-E: SIGHUP-driven TLS cert rotation. Apid
		// doesn't yet have its own hupCh (pkg/wire.Daemon's is consumed
		// by watchLogLevelReload). Install three parallel ones — each
		// gets every SIGHUP (signal.Notify fans the signal out to every
		// registered channel). Best-effort failure posture (matches
		// egress bundle): a failed reload keeps prior material live,
		// never bricks. WatchTLSReload returns immediately on ctx cancel.
		if cfg != nil && cfg.GithubdClientTLSCAPath != "" {
			githubdHupCh := make(chan os.Signal, 1)
			signal.Notify(githubdHupCh, syscall.SIGHUP)
			defer signal.Stop(githubdHupCh)
			githubdReload := func() (*tls.Config, error) {
				return cfg.LoadGithubdTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
			}
			go wire.WatchTLSReload(ctx, log, githubdHupCh, githubdRotator, githubdReload)
		}
		if cfg != nil && cfg.AdvisoryTLSCAPath != "" {
			advisoryHupCh := make(chan os.Signal, 1)
			signal.Notify(advisoryHupCh, syscall.SIGHUP)
			defer signal.Stop(advisoryHupCh)
			advisoryReload := func() (*tls.Config, error) {
				return cfg.LoadAdvisoryTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
			}
			go wire.WatchTLSReload(ctx, log, advisoryHupCh, advisoryRotator, advisoryReload)
		}
		if cfg != nil && cfg.GithubdBridgeTLSCAPath != "" {
			bridgeHupCh := make(chan os.Signal, 1)
			signal.Notify(bridgeHupCh, syscall.SIGHUP)
			defer signal.Stop(bridgeHupCh)
			bridgeReload := func() (*tls.Config, error) {
				return cfg.LoadGithubdBridgeTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
			}
			go wire.WatchTLSReload(ctx, log, bridgeHupCh, bridgeRotator, bridgeReload)
		}
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("apid listening", "addr", listenBind)
		if err := httpSrv.Serve(l); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
		_ = httpSrv.Shutdown(shutdownCtx)
		if metricsSrv != nil {
			//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
			_ = metricsSrv.Shutdown(shutdownCtx)
		}
		if advisorySrv != nil {
			// GracefulStop lets in-flight ForwardStatelessAdvisory
			// calls finish writing their audit row before exit.
			advisorySrv.GracefulStop()
		}
		if bridgeSrv != nil {
			// GracefulStop lets in-flight EnqueueBuild RPCs finish
			// writing the deployment + build rows before exit.
			// A non-graceful Stop would leave the githubd dispatcher
			// with a gRPC error mid-enqueue, which the dispatcher
			// handles with log + skip — but graceful is cheaper.
			bridgeSrv.GracefulStop()
		}
		if appErrSrv != nil {
			appErrSrv.GracefulStop()
		}
		// Issue #286: drain the async failed-login audit channel
		// so in-flight rows land in the events table before the
		// daemon exits. Close is idempotent — safe to call from
		// the shutdown path even if WithOpsMetrics wasn't wired
		// (the auditor's failedCh is nil and Close is a no-op).
		if srv.audit != nil {
			srv.audit.Close()
		}
		return nil
	}
}

// pgNotifier is the production Notifier — it just delegates to db.Notify.
type pgNotifier struct {
	pool *pgxpool.Pool
}

func (p pgNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.pool, channel, payload)
}

// Subscribe hands the SSE handler a live channel stream from the
// Postgres pool. Returns immediately if no channels are requested.
func (p pgNotifier) Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error) {
	return db.Subscribe(ctx, p.pool, channels)
}

// WaitFor is the Move 2 long-poll sibling: per-request LISTEN + predicate
// filter. Thin wrapper around db.WaitForNotification so the Notifier
// interface stays the only thing the handlers depend on.
func (p pgNotifier) WaitFor(ctx context.Context, channel string, predicate func(payload string) bool, timeout time.Duration) (string, error) {
	return db.WaitForNotification(ctx, p.pool, channel, predicate, timeout)
}

// auditHMACKeyFile is the on-disk fallback path for the audit HMAC
// key when FAAS_AUDIT_HMAC_KEY is unset (CodeQL alert #121, issue
// #286). The file is created on first boot with 0o600 perms and
// persists across daemon restarts so the audit-row email_hash
// values remain stable across boots (otherwise every restart would
// rotate the key and break the join-key contract — the same email
// would hash to a different value, fragmenting the audit table).
//
// 0o600 perms because the file IS a secret: anyone with read access
// can derive the audit HMAC key and rainbow-table the audit table.
// Mirrors the host.age identity perms (0o400 read-only; PR #237).
const auditHMACKeyFile = "/var/lib/faas/audit-hmac.key"

// auditHMACKeyEnvVar is the env var name an operator sets to
// supply the audit HMAC key explicitly (hex-encoded 32 bytes). The
// env-var path is the production-recommended route (Kubernetes
// Secret + envFromSecret, systemd EnvironmentFile=). The file
// fallback is the dev / single-node convenience.
const auditHMACKeyEnvVar = "FAAS_AUDIT_HMAC_KEY"

// auditHMACKeyFileEnvVar overrides the default auditHMACKeyFile
// path. Exists so the e2e harness and operator with non-standard
// state dirs can pin the path; tests use this to redirect to a
// tmp dir.
const auditHMACKeyFileEnvVar = "FAAS_AUDIT_HMAC_KEY_FILE"

// loadOrGenerateAuditHMACKey returns the per-daemon audit HMAC key
// per the precedence documented at the call site. Returns nil with
// no error if neither the env var nor the fallback file path yields
// a key — pkg/auth.SetHMACSecret accepts nil and logs a Warn, so the
// daemon still boots (in dev mode) with the zero-key fallback.
//
// Errors are returned only when:
//   - FAAS_AUDIT_HMAC_KEY is set but is not valid hex / wrong length.
//     A malformed key is a hard error: silently using a partial key
//     would let the operator think they have rainbow-table
//     resistance when they don't.
//   - The fallback file path is unreachable (perm denied, fs error).
//     Hard error: dev-mode auto-generation needs the file to
//     persist the key across restarts.
//
// The env var takes precedence over the file path even if the file
// is older / shorter — operator intent is explicit.
func loadOrGenerateAuditHMACKey(getenv func(string) string, log *slog.Logger) ([]byte, error) {
	// Precedence 1: env var (production path).
	if hexStr := getenv(auditHMACKeyEnvVar); hexStr != "" {
		key, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("decode %s hex: %w", auditHMACKeyEnvVar, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to exactly 32 bytes (got %d); use `openssl rand -hex 32` to generate", auditHMACKeyEnvVar, len(key))
		}
		log.Info("audit HMAC key loaded from env", "env", auditHMACKeyEnvVar)
		return key, nil
	}

	// Precedence 2: file path (dev-mode auto-generated).
	path := auditHMACKeyFile
	if override := getenv(auditHMACKeyFileEnvVar); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, decErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decErr != nil {
			return nil, fmt.Errorf("decode %s hex content: %w", path, decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s content must decode to exactly 32 bytes (got %d); delete the file to regenerate", path, len(key))
		}
		log.Info("audit HMAC key loaded from file", "path", path)
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Precedence 3: generate + persist (dev-mode first boot). Skip
	// silently if /var/lib/faas doesn't exist (typical for tests
	// running as non-root) — the zero-key fallback in pkg/auth
	// will catch it.
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		log.Warn("audit HMAC key file dir unavailable; running on zero-key fallback (HashEmail is rainbow-table-reversible)",
			"path", path, "err", statErr)
		return nil, nil
	}
	key, err := auth.GenerateHMACSecret()
	if err != nil {
		return nil, fmt.Errorf("generate audit HMAC key: %w", err)
	}
	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		log.Warn("audit HMAC key auto-persist failed; running on in-process key (HashEmail joins break on restart)",
			"path", path, "err", err)
		return key, nil
	}
	log.Info("audit HMAC key generated and persisted", "path", path)
	return key, nil
}

// recoveryHMACKeyFile is the on-disk fallback path for the
// recovery-code HMAC key. The loader writes to this file on first
// boot when neither the env var nor an existing file is present.
//
// 0o600 perms because the file IS a secret: anyone with read
// access can derive the recovery HMAC key and, combined with a
// leaked PG blob, can compute hashes offline. Mirrors the host.age
// identity perms (0o400 read-only; PR #237) and the audit HMAC key
// file convention.
const recoveryHMACKeyFile = "/var/lib/faas/recovery-hmac.key"

// recoveryHMACKeyEnvVar is the env var name an operator sets to
// supply the recovery HMAC key explicitly (hex-encoded 32 bytes).
// Production uses this — Kubernetes Secret + envFromSecret, or
// systemd EnvironmentFile=. The file fallback is the dev /
// single-node convenience.
const recoveryHMACKeyEnvVar = "FAAS_MFA_RECOVERY_HMAC_KEY"

// recoveryHMACKeyFileEnvVar overrides the default
// recoveryHMACKeyFile path. Exists so the e2e harness and operators
// with non-standard state dirs can pin the path; tests use this
// to redirect to a tmp dir.
const recoveryHMACKeyFileEnvVar = "FAAS_RECOVERY_HMAC_KEY_FILE"

// loadOrGenerateRecoveryHMACKey returns the per-daemon recovery
// HMAC key per the precedence documented at the call site. STRICTER
// than loadOrGenerateAuditHMACKey: returns an error if no key can
// be loaded — the audit-hmac.key path falls back to a zero-key
// (with a Warn) because HashEmail can degrade gracefully; recovery
// codes cannot, so refusing to start is the correct tradeoff.
//
// Errors are returned when:
//   - FAAS_MFA_RECOVERY_HMAC_KEY is set but is not valid hex / wrong
//     length (hard error; a partial key would silently disable the
//     rainbow-table defence).
//   - The fallback file path is set but unreadable / malformed.
//   - The file does not exist AND /var/lib/faas is writable AND
//     generation succeeds — the loader persists the key. (This is
//     the dev-mode first-boot path; failure here is a hard error
//     rather than a zero-key fallback.)
//   - The loader reaches the "no key" path: env unset, file
//     missing, /var/lib/faas missing — the loader returns an
//     explicit error so the boot fails. Test paths can supply a
//     writable tmp via FAAS_RECOVERY_HMAC_KEY_FILE.
func loadOrGenerateRecoveryHMACKey(getenv func(string) string, log *slog.Logger) ([]byte, error) {
	// Precedence 1: env var (production path).
	if hexStr := getenv(recoveryHMACKeyEnvVar); hexStr != "" {
		key, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("decode %s hex: %w", recoveryHMACKeyEnvVar, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to exactly 32 bytes (got %d); use `openssl rand -hex 32` to generate", recoveryHMACKeyEnvVar, len(key))
		}
		log.Info("recovery HMAC key loaded from env", "env", recoveryHMACKeyEnvVar)
		return key, nil
	}

	// Precedence 2: file path (dev-mode auto-generated; survives
	// daemon restart so the stored MFA recovery-code hashes remain
	// stable across boots).
	path := recoveryHMACKeyFile
	if override := getenv(recoveryHMACKeyFileEnvVar); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, decErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decErr != nil {
			return nil, fmt.Errorf("decode %s hex content: %w", path, decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s content must decode to exactly 32 bytes (got %d); delete the file to regenerate", path, len(key))
		}
		log.Info("recovery HMAC key loaded from file", "path", path)
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Precedence 3: generate + persist (dev-mode first boot). The
	// audit-HMAC loader tolerates /var/lib/faas being missing
	// (test runs as non-root); the recovery-HMAC loader does NOT
	// — refusing to start is the correct strict-mode behaviour.
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		return nil, fmt.Errorf("stat %s (refusing to start without a recovery HMAC key; set %s, %s, or symlink %s into a writable dir): %w",
			dir, recoveryHMACKeyEnvVar, recoveryHMACKeyFileEnvVar, recoveryHMACKeyFile, statErr)
	}
	key, err := authcode.GenerateRandomKey()
	if err != nil {
		return nil, fmt.Errorf("generate recovery HMAC key: %w", err)
	}
	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("persist %s (refusing to start with an in-process-only key — the next restart would force every MFA-enrolled customer to re-enroll): %w",
			path, err)
	}
	log.Info("recovery HMAC key generated and persisted", "path", path)
	return key, nil
}

// loadHostHMACKey loads the per-host HMAC key that
// secretbox.ValueFingerprint uses to compute the value-equality
// discriminator on the sealed envelope (ADR-117 env-diff matrix,
// PR-C). The file is RAW 32 bytes (NOT hex-encoded — the
// audit/recovery keys use hex for filesystem portability, but
// the env-diff key lives in /etc/faas/secrets alongside
// host.age.pub which is also raw bytes). Mode MUST be 0o400
// (root-only) OR 0o440 (group-readable under systemd
// credentials). Anything else — 0o600, 0o640, 0o644, 0o660,
// 0o664, 0o666, 0o777 — is rejected: anyone who can read the
// key can trivially compute value_hash for any observed
// plaintext (it's HMAC, not asymmetric), which would let a
// non-root reader cross-customer correlate the discriminator.
//
// Bootstrap (operator):
//
//	openssl rand -out /etc/faas/secrets/host.hmac.key 32
//	chmod 0400 /etc/faas/secrets/host.hmac.key
//	chown root:root /etc/faas/secrets/host.hmac.key
//
// Mirrors the existing age-recipient / age-identity loader
// posture at cmd/apid/main.go:1213,1242 (which check the same
// 0o400 perm). Returns a defensive copy of the bytes (NOT a
// reference to the on-disk file) so a future re-read of the
// file does not silently mutate the cache.
func loadHostHMACKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w (bootstrap with `openssl rand -out %s 32 && chmod 0400 %s && chown root:root %s`): %w",
			path, err, path, path, path, err)
	}
	mode := info.Mode().Perm()
	if mode != 0o400 && mode != 0o440 {
		return nil, fmt.Errorf("insecure file mode 0o%o (want 0o400 or 0o440) on %s — fix with `chmod 0400 %s` and `chown root:root %s`",
			mode, path, path, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) != 32 {
		return nil, fmt.Errorf("got %d bytes from %s, want exactly 32 (HMAC-SHA256 key length) — regenerate with `openssl rand -out %s 32`",
			len(data), path, path)
	}
	out := make([]byte, 32)
	copy(out, data)
	return out, nil
}

// runAdvisoryServer binds the AdvisoryService gRPC server onto a
// fresh /run/faas/apid.sock (or wherever FAAS_APID_ADVISORY_SOCK
// points). Single-box deployments point sock at a unix:// path;
// the owner is the apid daemon user (lookup via
// pkg/wire.ListenOrRecreateByName), the group is `faas` so vmmd can
// dial without root, and the mode is 0660 — the standing repo
// convention (pkg/wire.DefaultSocketMode). Multi-box deployments
// pass a tcp:// or dns:// target + a non-nil tlsCfg loaded via
// wire.LoadServerTLSConfig (ADR-052). Empty sock disables the
// listener entirely (matches the e2e harness path).
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are fatal — without the advisory listener vmmd has no way to
// forward fanotify batches and the audit loop is silently broken.
func runAdvisoryServer(ctx context.Context, target string, tlsCfg *tls.Config, store state.Store, audit *auditor, notif Notifier, log *slog.Logger, ops *wire.OpsMetrics) (*grpc.Server, net.Listener, error) {
	// Guard: a tcp/dns target without TLS would silently build an
	// insecure server (wire.Listen returns raw TCP, ServerCredsOrEmpty
	// yields zero opts). Refuse; the operator must set the
	// FAAS_APID_ADVISORY_TLS_{CERT,KEY,CA}_PATH env trio. ADR-052.
	if !isUnixSocketPath(target) && tlsCfg == nil {
		return nil, nil, fmt.Errorf(
			"advisory: target %q is non-unix but %s is empty (set FAAS_APID_ADVISORY_TLS_CERT_PATH / KEY_PATH / CA_PATH or point the target at a unix socket for single-box mode)",
			target, "FAAS_APID_ADVISORY_TLS_*_PATH")
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(target) {
		lis, err = wire.ListenOrRecreateByName(target, "faas-apid")
	} else {
		lis, err = wire.Listen(ctx, target, tlsCfg)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("advisory listen: %w", err)
	}
	srv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(tlsCfg),
		wire.TraceServerOptions()...,
	)...)
	// Mega-PR B: pass ops so the receiver can increment
	// apid_stateless_advisory_events_total on each landed advisory.
	// The accessor is nil-receiver safe so the metric stays zero
	// when ops is nil (test path).
	registerAdvisoryReceiver(srv, store, audit, notif, log, ops)
	return srv, lis, nil
}

// runGithubdBridgeServer binds the githubd → apid build-enqueue
// gRPC server onto a fresh /run/faas/apid-githubd.sock (or wherever
// FAAS_APID_GITHUBD_BRIDGE_SOCK points). The githubd daemon dials
// this listener after the dispatcher fans out the touched apps
// and stages each app's RootDir subtree into its build-sources
// dir as a per-app .tar.gz (issue #432 phase 5).
//
// The DAC contract mirrors the advisory socket (0660 group
// `faas`) so githubd can dial without root, but the listener is
// separately configurable so an operator can disable the bridge
// for a single-box deployment that doesn't run githubd. Empty
// sock disables the listener entirely (matches the e2e harness
// path + macOS dev boxes where /run/faas is read-only).
//
// The receiver implementation is in githubd_bridge.go. The set
// of state.Store methods the receiver needs is consumed through
// the githubdBridgeStore interface so unit tests can pass a stub
// without a real pgxpool. The store/notif/log/ops are passed
// through by the production wiring code in run().
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are fatal — without the bridge listener githubd has no
// way to enqueue builds and the dispatch path is silently
// degraded (every push hits the noopEnqueuer path).
func runGithubdBridgeServer(ctx context.Context, target string, tlsCfg *tls.Config, store githubdBridgeStore, notif githubdBridgeNotifier, log *slog.Logger, ops *wire.OpsMetrics, spool string, stagingRoot string) (*grpc.Server, net.Listener, error) {
	// Same multi-box guard as runAdvisoryServer — a tcp/dns
	// target without TLS would silently build an insecure server.
	// ADR-052.
	if !isUnixSocketPath(target) && tlsCfg == nil {
		return nil, nil, fmt.Errorf(
			"githubd bridge: target %q is non-unix but %s is empty (set FAAS_APID_GITHUBD_BRIDGE_TLS_CERT_PATH / KEY_PATH / CA_PATH or point the target at a unix socket for single-box mode)",
			target, "FAAS_APID_GITHUBD_BRIDGE_TLS_*_PATH")
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(target) {
		lis, err = wire.ListenOrRecreateByName(target, "faas-apid")
	} else {
		lis, err = wire.Listen(ctx, target, tlsCfg)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("githubd bridge listen: %w", err)
	}
	srv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(tlsCfg),
		wire.TraceServerOptions()...,
	)...)
	// Pass ops so the receiver can increment
	// apid_githubd_bridge_enqueued_total on each landed build.
	// The accessor is nil-receiver safe so the metric stays zero
	// when ops is nil (test path).
	registerGithubdBridge(srv, store, notif, log, ops, spool, stagingRoot)
	return srv, lis, nil
}

// isUnixSocketPath detects the legacy single-box dial target by
// checking for the unix:// scheme OR a bare absolute filesystem
// path (the historical FAAS_APID_ADVISORY_SOCK default). Anything
// else — host:port, dns://authority, tcp://host:port — is treated
// as a multi-box dial target that requires a non-nil tlsCfg.
func isUnixSocketPath(target string) bool {
	if strings.HasPrefix(target, "unix://") || strings.HasPrefix(target, "/") {
		return true
	}
	return false
}

// runAppErrorsServer brings up the AppErrors gRPC server
// (ADR-096 §3.5: gatewayd-internal → apid IncrementAppError
// streaming RPC). Unix targets retain the local DAC-authenticated
// single-box path; tcp:// targets use the same mTLS-only listener
// contract as the other split-box gRPC surfaces.
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are non-fatal: the caller logs and continues without the
// app_errors gRPC server (the apid HTTP listener still serves).
func runAppErrorsServer(ctx context.Context, target string, tlsCfg *tls.Config, store state.Store, ops *wire.OpsMetrics, log *slog.Logger) (*grpc.Server, net.Listener, error) {
	if !isUnixSocketPath(target) && tlsCfg == nil {
		return nil, nil, fmt.Errorf("app errors: target %q is non-unix but app_errors_tls_* is empty (mTLS is required)", target)
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(target) {
		path := strings.TrimPrefix(target, "unix://")
		lis, err = wire.ListenOrRecreateByName(path, "faas-apid")
	} else {
		lis, err = wire.Listen(ctx, target, tlsCfg)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("app errors listen: %w", err)
	}
	srv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(tlsCfg),
		wire.TraceServerOptions()...,
	)...)
	registerAppErrorsReceiver(srv, store, ops, true)
	// Split-box deployments reuse the same private mTLS listener for both
	// gateway telemetry services. Single-box deployments use the dedicated
	// request_telemetry.sock server below, preserving the separate DAC
	// boundaries for the legacy Unix sockets.
	if !isUnixSocketPath(target) && os.Getenv("FAAS_REQUEST_TELEMETRY_ENABLED") != "false" {
		registerRequestTelemetryReceiver(srv, store, ops, nil, true)
	}
	return srv, lis, nil
}

// runRequestTelemetryServer brings up the RequestTelemetry gRPC
// server (ADR-127 PR-B: gatewayd-internal → apid
// IncrementRequestTelemetry streaming RPC). Listens on a unix
// socket under /run/faas so the gateway-side dial is loopback-only
// and TLS-free (single-box mode). The socket path is hard-coded
// to /run/faas/request_telemetry.sock — gatewayd-internal dials
// it via FAAS_APID_REQUEST_TELEMETRY_SOCKET env (defaulting to
// the same path).
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are non-fatal: the caller logs and continues without the
// request_telemetry gRPC server (the apid HTTP listener still
// serves).
func runRequestTelemetryServer(ctx context.Context, store state.Store, ops *wire.OpsMetrics, log *slog.Logger, limiter *peraccount.Limiter) (*grpc.Server, net.Listener, error) {
	const sock = "/run/faas/request_telemetry.sock"
	lis, err := wire.ListenOrRecreateByName(sock, "faas-apid")
	if err != nil {
		return nil, nil, fmt.Errorf("request telemetry listen: %w", err)
	}
	srv := grpc.NewServer()
	registerRequestTelemetryReceiver(srv, store, ops, limiter, true)
	return srv, lis, nil
}

// runSpansWriterServer brings up the SpansWriter gRPC server
// (ADR-127 PR-D: gatewayd-public → apid WriteSpansSummary
// unary RPC). Listens on /run/faas/otel_spans_writer.sock so
// the gateway-side dial is loopback-only and TLS-free
// (single-box mode). The socket path is hard-coded —
// gatewayd-public dials it via FAAS_APID_OTEL_SPANS_WRITER_SOCKET
// env (defaulting to the same path).
//
// Returns the server (caller calls Serve) and the listener.
// Errors here are non-fatal: the caller logs and continues
// without the spans_writer gRPC server (the apid HTTP listener
// still serves; the gateway's OTel flush loop drops the entry
// at the next tick).
func runSpansWriterServer(ctx context.Context, store state.Store, ops *wire.OpsMetrics, log *slog.Logger, limiter *peraccount.Limiter) (*grpc.Server, net.Listener, error) {
	const sock = "/run/faas/otel_spans_writer.sock"
	lis, err := wire.ListenOrRecreateByName(sock, "faas-apid")
	if err != nil {
		return nil, nil, fmt.Errorf("otel spans writer listen: %w", err)
	}
	srv := grpc.NewServer()
	registerSpansWriterReceiver(srv, store, ops, limiter, true)
	return srv, lis, nil
}

// runAuthServer brings up the Auth gRPC server (ADR-127 PR-D:
// gatewayd-public → apid AuthenticateKey unary RPC). Listens on
// /run/faas/auth.sock so the gateway-side dial is loopback-only
// and TLS-free (single-box mode). The socket path is hard-coded
// — gatewayd-public dials it via FAAS_APID_AUTH_SOCKET env.
//
// Returns the server (caller calls Serve) and the listener.
// Errors here are non-fatal: the caller logs and continues
// without the auth gRPC server (the apid HTTP listener still
// serves; the gateway's OTel handler refuses to start without
// auth, so a missing auth server == a missing OTel surface,
// which is the desired fail-closed posture).
func runAuthServer(ctx context.Context, store state.Store, log *slog.Logger) (*grpc.Server, net.Listener, error) {
	const sock = "/run/faas/auth.sock"
	lis, err := wire.ListenOrRecreateByName(sock, "faas-apid")
	if err != nil {
		return nil, nil, fmt.Errorf("auth listen: %w", err)
	}
	srv := grpc.NewServer()
	registerAuthReceiver(srv, store)
	return srv, lis, nil
}
