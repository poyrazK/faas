// Command builderd — build orchestrator + ephemeral builder microVMs (spec
// §4.5, ADR-003, ADR-005).
//
// builderd consumes `build_queued` notifications emitted by apid when a
// source tarball is uploaded, claims the build row, and runs it inside an
// ephemeral Firecracker microVM (or short-circuits via the content-addressed
// cache). The produced app-layer ext4 is stamped onto the deployment row;
// from there the existing imaged→schedd snapshot_prime handshake takes over.
//
// wiring follows the schedd/apid runDeps pattern: production uses defaultDeps,
// tests swap fields. The metal VM driver is selected at build time via the
// `metal` build tag (vm_metal.go vs vm_stub.go).
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"

	builderdpkg "github.com/onebox-faas/faas/pkg/builderd"
)

func main() {
	wire.Daemon("builderd", run)
}

// runDeps is the DI seam for run. Production uses the defaults; tests swap
// fields to drive run without Postgres or vmmd.
type runDeps struct {
	configPath       string
	openDB           func(context.Context, string) (*pgxpool.Pool, error)
	migrate          func(context.Context, *pgxpool.Pool) error
	newDriver        func(ctx context.Context, target string, tlsCfg *tls.Config, builderBase, driveDir, exportDir string) (builderdpkg.VM, error)
	newResidentProbe func(ctx context.Context, url string) builderdpkg.ResidencyProbe
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check (the test runner doesn't carry
	// the production capset).
	capCheck func() error
}

func defaultDeps() runDeps {
	return runDeps{
		// FAAS_BUILDERD_CONFIG lets the e2e harness (and operators) point
		// at a writable per-test config in /tmp rather than the immutable
		// /etc/faas/builderd.toml on the EX44. Mirrors FAAS_SCHEDD_CONFIG,
		// FAAS_VMMD_CONFIG (cmd/schedd, cmd/vmmd).
		configPath: envOr("FAAS_BUILDERD_CONFIG", "/etc/faas/builderd.toml"),
		// OpenWithAppName tags every connection — including the
		// long-lived LISTEN one — with application_name=faas-builderd
		// so the e2e harness (and operators) can identify this daemon
		// in pg_stat_activity without grepping query text.
		openDB: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
			return db.OpenWithAppName(ctx, dsn, "faas-builderd")
		},
		migrate: db.MigrateUp, // F2 / ADR-124: acquires pg_advisory_lock; safe for fleet bootstrap
		// newDriver is set per build tag at link time: metal uses vmmd
		// over gRPC; non-metal uses the stub that returns ErrNotMetal.
		// The *Context form (issue #95) threads ctx + tlsCfg through to
		// wire.DialContext.
		newDriver: func(ctx context.Context, target string, tlsCfg *tls.Config, builderBase, driveDir, exportDir string) (builderdpkg.VM, error) {
			return builderdpkg.NewVMMDriverContext(ctx, target, tlsCfg, builderBase, driveDir, exportDir)
		},
		// newResidentProbe wires the 2nd-slot gate (spec §4.5, §13). The
		// default polls schedd's /metrics endpoint on cfg.ScheddMetricsURL;
		// tests can inject a fixed probe to drive slot decisions without
		// standing up schedd.
		newResidentProbe: builderdpkg.NewMetricsResident,
	}
}

// envOr returns os.Getenv(key) when set, otherwise fallback. Mirrors the
// helper in cmd/schedd/main.go and cmd/imaged/main.go.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func builderNotificationChannels() []string {
	return []string{db.NotifyBuildQueued, db.NotifyBuildChanged}
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	// DEPLOY-1 / ADR-075 capdecl gate. builderd is unprivileged —
	// no Allow, no Deny. The build queue consumer, the vmmd
	// gRPC dial, the content-addressed cache read/write, and
	// the Postgres migrations all run inside the unit's
	// systemd hardening (NoNewPrivileges, ProtectSystem,
	// PrivateTmp, etc.). The ephemeral builder microVM itself
	// (firecracker + jailer) is owned by vmmd, not builderd;
	// vmmd's capsDecl is the gating authority for any cap_ the
	// VM lifecycle needs. The capCheck seam (review finding
	// M2) lets tests stub the live /proc/self/status check.
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("builderd")
	traceShutdown, traceErr := trace.InitTracerWithRegistry(ctx, "builderd", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	if traceErr != nil {
		return fmt.Errorf("builderd: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("builderd: trace shutdown failed", "err", err)
		}
	}()

	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	var sourceStorage storage.StorageBackend
	if os.Getenv("FAAS_STORAGE_BACKEND") == "oci" {
		sourceStorage, err = storage.BackendFromEnv()
		if err != nil {
			return fmt.Errorf("builderd: source storage: %w", err)
		}
		log.Info("source storage enabled", "backend", "oci")
	}
	// Gate-B box-role gate. builderd is a compute-only daemon —
	// it refuses to start under RoleControlPlane. The role is
	// set from TOML or FAAS_BUILDERD_ROLE at deploy time;
	// default is RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("builderd", cfg.Role, role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	vmmTarget := cfg.ResolveVMMTarget()
	log.Info("config",
		"vmmd_target", vmmTarget,
		"vmmd_socket", cfg.VMMDSocket)

	pool, err := deps.openDB(ctx, cfg.DBURL)
	if err != nil {
		return fmt.Errorf("builderd: open db: %w", err)
	}
	defer pool.Close()
	if err := deps.migrate(ctx, pool); err != nil {
		return err
	}

	// Issue #571 / PR-A2: builderd /readyz probe. Three signals —
	// PG ping (queued-build backlog stays reachable), vmmd RPC
	// dialable (the build-volume microVM host), and
	// cfg.BuildDriveDir writable (the overlay mount source).
	// Constructed after openDB so the pool is live; the vmmd
	// dial signal races alongside the driver dial below.

	// Issue #95 / ADR-025: dial vmmd through the location-transparent
	// helper. tcp/dns targets require the tls_* cluster; nil TLS on a
	// unix target keeps single-box behaviour unchanged.
	vmmTLS, err := cfg.LoadVMMTLS()
	if err != nil {
		return fmt.Errorf("builderd: load vmmd TLS: %w", err)
	}
	builderdProbe := buildReadinessProbeForDrive(ctx, pool, cfg.BuildDriveDir, vmmTarget, tlsReadinessDialer(vmmTLS))

	driver, err := deps.newDriver(ctx, vmmTarget, vmmTLS, cfg.BuilderBase, cfg.BuildDriveDir, cfg.BuildExportDir)
	if err != nil {
		return fmt.Errorf("builderd: vmmd driver: %w", err)
	}
	if c, ok := driver.(*builderdpkg.VMMDriver); ok {
		defer func() { _ = c.Close() }()
	}

	store := state.NewPgStore(pool)
	notif := dbNotifier{pool: pool}
	resid := deps.newResidentProbe(ctx, cfg.ScheddMetricsURL)
	// Single OpsMetrics for the daemon: builderd both records build
	// telemetry on it (ObserveBuild*) and serves it at /metrics. Building
	// it once (not inline in the /metrics block) is what makes the build
	// series real rather than a throwaway (ADR-030).
	wire.BootStamps(ctx, "builderd", ops)
	wire.RegisterDefaultOps(ops)
	builderdProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("builderd", ready, reason)
	})
	// issue #517 / PR-C / ADR-064: thread the events Platform
	// so markSucceeded / markFailed emit
	// wake.build_succeeded / wake.build_failed on the events
	// table. nil opts out (the cmd/builderd tests still drive
	// Builderd without an events Platform). The Platform writes
	// the events row + bumps wake_phase_emitted_total.
	eventsPlatform := events.NewPlatform("builderd", store, log, ops, nil)
	cache := builderdpkg.NewCache(cfg.CacheDir).WithLeaseReferenceChecker(
		func(ctx context.Context, deploymentID, leasePath string) (bool, error) {
			dep, err := store.DeploymentByID(ctx, deploymentID)
			if errors.Is(err, state.ErrNotFound) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return dep.RootfsPath == leasePath, nil
		},
	)
	b := builderdpkg.New(store, notif, driver, cache, nil, resid, builderdpkg.Config{
		CacheDir:            cfg.CacheDir,
		MetricsAddr:         cfg.MetricsAddr,
		BuildTimeoutSeconds: cfg.BuildTimeoutSeconds,
		FairnessWindow:      cfg.FairnessWindow,
		// ADR-038: BuilderNodeID is stamped onto every
		// build_provenance row builderd writes. Defaulted to
		// "default-local" in LoadConfig; multi-node deployments
		// override per-builder via the toml field.
		BuilderNodeID: cfg.BuilderNodeID,
	}, log).WithOpsMetrics(ops).WithEvents(eventsPlatform).WithSourceStorage(sourceStorage)
	notifCh, err := db.SubscribeWithReconnect(ctx, pool, builderNotificationChannels(), log)
	if err != nil {
		return err
	}
	// SubscribeWithReconnect owns its own cancel inside the wrapper.
	// Start the independent builder liveness signal only after all
	// database-backed startup wiring succeeds, so a failed boot cannot
	// leave a heartbeat goroutine using a pool that is about to close.
	go builderHeartbeatLoop(ctx, store, builderHeartbeatNodeName(cfg), defaultBuilderHeartbeatInterval, log)

	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", ops.Handler())
		// Issue #571 / PR-A2: operator-side /healthz + /readyz on
		// the metrics mux. ControlMuxLite is the canonical
		// shape — /readyz returns 503 with the failing reason
		// when the probe is degraded. Customer-facing routes
		// are on the apid mux (cmd/apid/handlers_ready.go).
		wire.ControlMuxLite(mux, builderdProbe.ReadyFunc(), builderdProbe.ReasonFunc())
		// ADR-122: apply the canonical metrics-listener shape —
		// RT/WT/IT/MHB from cfg.MetricsListener (cfg → constant
		// fallback). ReadHeaderTimeout=10s stays from before ADR-122.
		readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := cfg.MetricsListener()
		httpSrv = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    int(maxHeaderBytes),
		}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("builderd: metrics http", "err", err)
			}
		}()
		log.Info("builderd: metrics listening", "addr", cfg.MetricsAddr)
	}

	log.Info("builderd ready",
		"vmmd_target", vmmTarget,
		"cache_dir", cfg.CacheDir,
		"poll_interval", cfg.PollInterval)

	// PR-B: durable worker. LISTEN/NOTIFY above is the fast path
	// (apid emits on build_queued immediately after CreateBuild); this
	// worker is the recovery net for missed notify / apid crashed
	// mid-deploy / Postgres-restart windows. It polls the queue with
	// SELECT … FOR UPDATE SKIP LOCKED via store.ClaimNextQueuedBuild
	// (the same SQL the LISTEN path eventually runs when the
	// notification reaches us, so an apid-emit + a worker-poll both
	// racing the same row is CAS-safe — one wins, the other gets
	// ErrNotFound and sleeps).
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	go workerLoop(ctx, b, pollInterval, log)

	// Stuck-running build reaper (issue #195 B1.4). Free-function
	// goroutine next to workerLoop; cadence + threshold come from
	// cfg with sensible defaults set by LoadConfig. Sweeps orphaned
	// 'running' rows that bypassed markSucceeded/markFailed (builder
	// VM crash, OOM-kill, kernel panic).
	reapInterval := cfg.StuckBuildSweepInterval
	if reapInterval <= 0 {
		reapInterval = 10 * time.Minute
	}
	reapThreshold := cfg.StuckBuildThreshold
	if reapThreshold <= 0 {
		reapThreshold = 15 * time.Minute
	}
	go builderdpkg.ReaperLoop(ctx, store, reapInterval, reapThreshold, log)

	// Build cache GC (issue #196 B2.1). Content-addressed cache at
	// cfg.CacheDir grows forever as builds accumulate; a daily sweep
	// enforces TTL + size cap (defaults: 30 days, 50 GiB). The sweep
	// shares the Cache instance used by build workers so lookup+lease,
	// Store and Sweep cannot race over the same entry.
	gcInterval := cfg.CacheGCSweepInterval
	if gcInterval <= 0 {
		gcInterval = 24 * time.Hour
	}
	go builderdpkg.CacheGCSweepLoop(ctx, cache, gcInterval, cfg.CacheMaxBytes, cfg.CacheMaxAge, log)

	// Split-box source retention. apid publishes sources/<build>.tar.gz
	// before it creates the durable build row, so an apid crash can leave a
	// remote object with no database owner. The sweep preserves queued and
	// running builds and removes terminal/orphaned objects after the bounded
	// retention window. Local single-box deployments have no sourceStorage,
	// so this loop is a no-op there.
	if sourceStorage != nil {
		sourceGCInterval := cfg.SourceGCSweepInterval
		if sourceGCInterval <= 0 {
			sourceGCInterval = 24 * time.Hour
		}
		sourceMaxAge := cfg.SourceMaxAge
		if sourceMaxAge <= 0 {
			sourceMaxAge = 24 * time.Hour
		}
		go builderdpkg.SourceGCSweepLoop(ctx, sourceStorage, store, sourceGCInterval, sourceMaxAge, log)
	}

	for {
		select {
		case <-ctx.Done():
			// ADR-122 / builderd shutdown fix: drain the
			// metrics listener so a cancel doesn't leak
			// an open *http.Server. 5s matches the meterd
			// precedent at cmd/meterd/main.go:978-983.
			// net/http Shutdown requires a non-Done parent
			// ctx — branch off Background here.
			if httpSrv != nil {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
				//nolint:contextcheck // shutdown ctx must outlive the already-cancelled caller ctx.
				_ = httpSrv.Shutdown(stopCtx)
				stopCancel()
			}
			return nil
		case n, ok := <-notifCh:
			if !ok {
				return nil
			}
			if n.Channel != db.NotifyBuildQueued {
				if n.Channel == db.NotifyBuildChanged {
					// Destroy may wait for vmmd to reap a VM and flush its
					// export. Keep the LISTEN loop available for other
					// cancellations and queued builds while that happens.
					go handleBuildCancelled(ctx, driver, n.Payload, log)
				}
				continue
			}
			var p struct {
				Build string `json:"build"`
			}
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				log.Warn("builderd: bad build_queued payload", "err", err)
				continue
			}
			if p.Build == "" {
				log.Warn("builderd: build_queued missing build id", "payload", n.Payload)
				continue
			}
			// Keep the LISTEN loop responsive while a build waits for
			// vmmd.Destroy/export. A synchronous ProcessOne would prevent
			// the build_changed notification from being read, defeating
			// cancellation for the exact in-flight build it is meant to stop.
			go func(buildID string) {
				if _, err := b.ProcessOne(ctx, buildID); err != nil {
					log.Warn("builderd: process", "build", buildID, "err", err)
				}
			}(p.Build)
		}
	}
}

// handleBuildCancelled is the ADR-124 build-cancel listener
// (cmd/builderd/main.go LISTEN goroutine). The pgstore row flip
// already happened inside CancelDeploymentTx; this function's only
// job is to ask the VM driver to drop the in-flight VM. The
// fire-and-forget shape is deliberate — a Cancel error is logged
// at WARN and the orphan is left for the ReaperLoop
// (pkg/builderd/reaper.go) to sweep. We never bubble up an error:
// the LISTEN goroutine must keep draining the channel.
func handleBuildCancelled(ctx context.Context, driver any, payload string, log *slog.Logger) {
	var p struct {
		BuildID string `json:"build_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		log.Warn("builderd: bad build_changed payload", "err", err)
		return
	}
	if p.BuildID == "" {
		log.Warn("builderd: build_changed missing build_id", "payload", payload)
		return
	}
	vm, ok := driver.(builderdpkg.VM)
	if !ok || vm == nil {
		// vm is the unit-test stub (interface{} nil) — nothing to do.
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := vm.Cancel(cctx, p.BuildID); err != nil {
		log.Warn("builderd: build cancel", "build", p.BuildID, "err", err)
	}
}

// workerLoop is the durable build-queue worker (PR-B). On each tick it
// calls store.ClaimNextQueuedBuild (SELECT … FOR UPDATE SKIP LOCKED
// inside the store). On hit it invokes ProcessNext and re-queues the
// build row on ErrNoSlot so the row preserves its FIFO position
// until a builder slot opens. Empty queue (ErrNotFound) is the
// expected idle state — no log noise. Errors get logged at WARN and
// the next tick retries; ctx cancel exits cleanly.
//
// Cadence is set by the caller (FAAS_BUILDER_POLL_INTERVAL, default
// 2 s); we hand-roll time.NewTicker rather than re-using imaged's
// WithGCChannel seam because the worker is short and the seam
// doesn't pay for itself here.
func workerLoop(ctx context.Context, b *builderdpkg.Builderd, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		_, err := b.ProcessNext(ctx)
		if err == nil {
			continue
		}
		// Distinguish the two "nothing to do" cases at the worker's
		// eye line: an empty queue is normal idle (claim itself
		// surfaces ErrNotFound, no build row was ever touched); a
		// vanished-row means we CAS-claimed a build row but its
		// parent deployment/app was concurrently deleted between
		// claim and load — a real anomaly worth a WARN so an
		// operator can correlate it with an apid/schedd event.
		// state.ErrNotFound matches both because ProcessNext
		// wraps the inner DeploymentByID/AppByID misses with %w.
		// Empty queue: claim returned ErrNotFound unwrapped. Log
		// signature: errors.Is is true AND the err chain has no
		// "load deployment"/"load app" wrap (those are the vanished
		// row markers).
		if errors.Is(err, state.ErrNotFound) {
			switch {
			case isVanishedRowErr(err):
				log.Warn("builderd: worker tick — vanished row (deployment/app deleted mid-claim)", "err", err)
			default:
				log.Debug("builderd: worker tick — queue empty")
			}
			continue
		}
		// ErrNoSlot is the "slot budget exhausted" state —
		// processClaimedBuild already requeued the row (preserving
		// its FIFO enqueued_at), so the worker just waits for the
		// next tick without logging warn.
		if errors.Is(err, builderdpkg.ErrNoSlot) {
			log.Debug("builderd: worker tick — no slot, row requeued")
			continue
		}
		log.Warn("builderd: worker tick — process next", "err", err)
	}
}

// isVanishedRowErr reports whether a state.ErrNotFound matched by the
// caller came from processClaimedBuild's DeploymentByID/AppByID load
// (i.e. the build row was claimed but its parents vanished) instead
// of an empty queue. The wrap strings are stable — see
// pkg/builderd/builderd.go::processClaimedBuild. Using strings rather
// than sentinel errors keeps the original error stream readable in
// logs (the wrap is what an operator would grep for).
func isVanishedRowErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "builderd: load deployment") ||
		strings.Contains(s, "builderd: load app")
}

// dbNotifier adapts *pgxpool.Pool to builderdpkg.Notifier.
type dbNotifier struct{ pool *pgxpool.Pool }

func (d dbNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, d.pool, channel, payload)
}
