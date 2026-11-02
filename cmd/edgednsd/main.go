// Command edgednsd keeps the app hosts' proxied address records in step with
// the compute nodes whose edges are ready (ADR-20261102081618 §3).
//
// It runs on the control plane. It holds the zone's DNS: Edit token, which
// never reaches a compute node or an internet-facing process, and listens
// only for metrics. Every tick it reads the node registry, probes each
// node's edge on the private network, and lets pkg/edgedns move the records.
// It starts in dry run: it logs and counts what it would change until
// FAAS_EDGEDNS_DRY_RUN=0.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/edgedns"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("edgednsd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("edgednsd", role.FromConfig("", "FAAS_EDGEDNS_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	token, err := os.ReadFile(cfg.tokenFile)
	if err != nil {
		return fmt.Errorf("edgednsd: read the Cloudflare token: %w", err)
	}
	dns, err := edgedns.NewCloudflare(edgedns.CloudflareAPI, strings.TrimSpace(string(token)))
	if err != nil {
		return fmt.Errorf("edgednsd: %w", err)
	}
	pool, err := db.OpenWithAppName(ctx, "", "faas-edgednsd")
	if err != nil {
		return fmt.Errorf("edgednsd: open database: %w", err)
	}
	defer pool.Close()

	ops := wire.NewOpsMetrics("edgednsd")
	metrics := edgedns.NewMetrics(ops.Registry())
	defer serveMetrics(ctx, cfg.metricsAddr, ops, log)()
	rec, err := edgedns.NewReconciler(edgedns.Config{Names: cfg.names, DryRun: cfg.dryRun}, dns, metrics, log)
	if err != nil {
		return fmt.Errorf("edgednsd: %w", err)
	}
	source := nodeSource{
		registry: state.NewPgStore(pool),
		probe:    edgedns.HTTPProbe{Client: &http.Client{Timeout: edgedns.ProbeTimeout}},
		public:   cfg.public,
		edgePort: cfg.edgePort,
	}
	log.Info("edgednsd: running", "names", len(cfg.names), "nodes", len(cfg.public), "dry_run", cfg.dryRun, "interval", cfg.interval)

	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for {
		tick(ctx, source, rec, log)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// tick is one reconcile. A failed registry read or record write is logged and
// retried on the next tick; the records stay as they are meanwhile.
func tick(ctx context.Context, source nodeSource, rec *edgedns.Reconciler, log *slog.Logger) {
	nodes, err := source.nodes(ctx)
	if err != nil {
		log.Warn("edgednsd: read the node registry", "err", err)
		return
	}
	if err := rec.Step(ctx, nodes); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("edgednsd: reconcile", "err", err)
	}
}

func serveMetrics(ctx context.Context, addr string, ops *wire.OpsMetrics, log *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", ops.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("edgednsd: metrics http", "err", err)
		}
	}()
	log.Info("edgednsd: metrics listening", "addr", addr)
	return func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}
}
