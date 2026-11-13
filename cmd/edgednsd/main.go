// Command edgednsd keeps the app hosts' address records in step with the
// compute nodes whose edges are ready (ADR-20261102081618 §3), proxied or
// DNS-only per name (ADR-20261103163954 §1).
//
// It runs on the control plane. It holds the zone's DNS: Edit token, which
// never reaches a compute node or an internet-facing process, and listens
// only for metrics. Every tick it reads the node registry, probes each
// node's edge on the private network, shakes hands with each node's public
// address for every DNS-only name, and lets pkg/edgedns move the records.
// It starts in dry run: it logs and counts what it would change until
// FAAS_EDGEDNS_DRY_RUN=0.
//
// With FAAS_EDGEDNS_CERT_NAMES set it also keeps the apps domains'
// publicly trusted certificate (ADR-20261103163954 §3): it orders it from
// Let's Encrypt with the DNS-01 challenge, through the same token, and
// stores it with its key sealed to the compute edges. With
// FAAS_EDGEDNS_CT_DOMAIN set it watches Certificate Transparency for
// certificates under that domain it did not issue.
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
	"github.com/onebox-faas/faas/pkg/edgecert"
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
		private:  cfg.private,
		edgePort: cfg.edgePort,
		// A staging certificate chains to a root no client trusts; the probe
		// then checks the names alone.
		direct:      edgedns.TLSProbe{NameOnly: cfg.cert.enabled() && cfg.cert.ca == edgecert.CALetsEncryptStaging},
		directHosts: dnsOnlyHosts(cfg.names),
	}
	certStore := edgecert.NewPgStore(pool)
	if cfg.cert.enabled() {
		certs, err := newCertManager(cfg.cert, dns, certStore, edgecert.NewMetrics(ops.Registry()), log)
		if err != nil {
			return err
		}
		go certs.Run(ctx)
		log.Info("edgednsd: certificate on", "names", strings.Join(cfg.cert.names, ","), "ca", cfg.cert.ca)
	}
	if cfg.watch.enabled() {
		watch, err := newTransparency(cfg.watch, certStore, edgecert.NewTransparencyMetrics(ops.Registry()), log)
		if err != nil {
			return err
		}
		go watch.Run(ctx)
		log.Info("edgednsd: certificate transparency watch on", "domain", cfg.watch.domain, "allow", strings.Join(cfg.watch.allow, ","), "api_key", cfg.watch.apiKeyFile != "")
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

// dnsOnlyHosts lists the DNS-only names, whose clients connect to the nodes'
// public addresses themselves.
func dnsOnlyHosts(names []edgedns.Name) []string {
	var hosts []string
	for _, name := range names {
		if name.DNSOnly {
			hosts = append(hosts, name.Host)
		}
	}
	return hosts
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
