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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
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
		migrate: db.MigrateUp,
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

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
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

	// Issue #95 / ADR-025: dial vmmd through the location-transparent
	// helper. tcp/dns targets require the tls_* cluster; nil TLS on a
	// unix target keeps single-box behaviour unchanged.
	vmmTLS, err := cfg.LoadVMMTLS()
	if err != nil {
		return fmt.Errorf("builderd: load vmmd TLS: %w", err)
	}
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
	ops := wire.NewOpsMetrics("builderd")
	b := builderdpkg.New(store, notif, driver, nil, nil, resid, builderdpkg.Config{
		CacheDir:    cfg.CacheDir,
		MetricsAddr: cfg.MetricsAddr,
	}, log).WithOpsMetrics(ops)

	notifCh, err := db.SubscribeWithReconnect(ctx, pool, []string{
		db.NotifyBuildQueued,
	}, log)
	if err != nil {
		return err
	}
	// SubscribeWithReconnect owns its own cancel inside the wrapper.

	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", ops.Handler())
		httpSrv = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
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

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notifCh:
			if !ok {
				return nil
			}
			if n.Channel != db.NotifyBuildQueued {
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
			if _, err := b.ProcessOne(ctx, p.Build); err != nil {
				log.Warn("builderd: process", "build", p.Build, "err", err)
			}
		}
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
		// Empty queue is the expected idle state. Log only at debug
		// so we don't drown the logs on a quiet box.
		if errors.Is(err, state.ErrNotFound) {
			log.Debug("builderd: worker tick — queue empty")
			continue
		}
		// ErrNoSlot means ProcessNext claimed the row, hit
		// DecideSlot's denial, marked it failed, and returned
		// ErrNoSlot. Wait — ProcessOne still calls markFailed on
		// no-slot. The worker cannot rely on the row staying queued
		// unless we requeue here BEFORE ProcessNext fails it. The
		// actual requeue lives in ProcessNext's no-slot path via
		// store.RequeueBuild (see the PR-B doc-comment there).
		if errors.Is(err, builderdpkg.ErrNoSlot) {
			log.Debug("builderd: worker tick — no slot, row requeued")
			continue
		}
		log.Warn("builderd: worker tick — process next", "err", err)
	}
}

// dbNotifier adapts *pgxpool.Pool to builderdpkg.Notifier.
type dbNotifier struct{ pool *pgxpool.Pool }

func (d dbNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, d.pool, channel, payload)
}
