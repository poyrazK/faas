// Command agentd runs Gregale Agents runs (ADR-20261027090010, ADR-20261027090014 §1): it claims
// queued agent runs and drives each through a schedd's RunAgent stream. Run
// several replicas; they share the queue through FOR UPDATE SKIP LOCKED
// leases. agentd stays idle unless FAAS_AGENTD_AGENT_RUNS=1 (ADR-20261027090010 §1).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("agentd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	if strings.TrimSpace(os.Getenv("FAAS_AGENTD_AGENT_RUNS")) != "1" {
		log.Info("agentd: agent runs are off (FAAS_AGENTD_AGENT_RUNS); idle")
		<-ctx.Done()
		return nil
	}
	cfg, target, tlsCfg, err := configFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, "", "faas-agentd")
	if err != nil {
		return fmt.Errorf("agentd: open database: %w", err)
	}
	defer pool.Close()
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return fmt.Errorf("agentd: dial schedd %q: %w", target, err)
	}
	defer func() { _ = conn.Close() }()
	ops := wire.NewOpsMetrics("agentd")
	metrics, err := agentd.NewMetrics(ops.Registry())
	if err != nil {
		return fmt.Errorf("agentd: register metrics: %w", err)
	}
	if addr := strings.TrimSpace(os.Getenv("FAAS_AGENTD_METRICS_ADDR")); addr != "" {
		stopMetrics := serveMetrics(ctx, addr, ops, log)
		defer stopMetrics()
	}
	store := agentstore.New(pool)
	replica := agentd.New(store, scheddpb.NewScheddClient(conn), cfg, log)
	closeDeps, err := withOptionalFeatures(ctx, os.Getenv, replica, features{store: store, grants: state.NewPgStore(pool), metrics: metrics}, log)
	if err != nil {
		return err
	}
	defer closeDeps()
	log.Info("agentd: running agent runs", "owner", cfg.Owner, "schedd", target, "concurrency", cfg.Concurrency)
	return replica.Run(ctx)
}

// features are what agentd's optional features are built from.
type features struct {
	store   *agentstore.Store
	grants  agentd.BucketGrants
	metrics *agentd.Metrics
}

// withOptionalFeatures turns on what the environment asks for: checkouts
// (FAAS_AGENTD_CHECKOUTS), output file contents (FAAS_AGENTD_OUTPUT_FILES)
// and deliveries (FAAS_AGENTD_DELIVERIES). Checkouts, output files and
// bucket deliveries use the artifact store; checkouts and GitHub deliveries
// use githubd. Bucket deliveries also need the S3 gateway's https origin in
// FAAS_AGENTD_S3_ENDPOINT.
func withOptionalFeatures(ctx context.Context, getenv func(string) string, replica *agentd.Agentd, f features, log *slog.Logger) (func(), error) {
	on := func(name string) bool { return strings.TrimSpace(getenv(name)) == "1" }
	checkouts, outputs, deliveries := on("FAAS_AGENTD_CHECKOUTS"), on("FAAS_AGENTD_OUTPUT_FILES"), on("FAAS_AGENTD_DELIVERIES")
	endpoint := strings.TrimSpace(getenv("FAAS_AGENTD_S3_ENDPOINT"))
	buckets := deliveries && endpoint != ""
	var backend storage.StorageBackend
	if checkouts || outputs || buckets {
		var err error
		if backend, err = storage.BackendFromEnvContext(ctx); err != nil {
			return nil, fmt.Errorf("agentd: artifact store: %w", err)
		}
	}
	var github *githubdgrpc.Client
	if checkouts || deliveries {
		var err error
		if github, err = dialGithubd(ctx, getenv); err != nil {
			return nil, err
		}
	}
	if checkouts {
		replica.WithCheckouts(&agentd.Checkouts{Source: github, Storage: backend})
		log.Info("agentd: checkouts enabled")
	}
	if outputs {
		replica.WithOutputFiles(&agentd.OutputFiles{Storage: backend})
		log.Info("agentd: output files enabled")
	}
	if deliveries {
		deliverer := agentd.NewDeliverer(f.store, github, agentd.DeliveryConfig{}, log).WithMetrics(f.metrics)
		if buckets {
			deliverer.WithBuckets(&agentd.Buckets{Grants: f.grants, Files: backend, Endpoint: endpoint,
				HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
		}
		replica.WithDeliveries(deliverer)
		log.Info("agentd: deliveries enabled", "buckets", buckets)
	}
	return func() {
		if github != nil {
			_ = github.Close()
		}
	}, nil
}

// serveMetrics serves /metrics on addr, a loopback or node-private address
// the node's Prometheus scrapes.
func serveMetrics(ctx context.Context, addr string, ops *wire.OpsMetrics, log *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", ops.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("agentd: metrics http", "err", err)
		}
	}()
	log.Info("agentd: metrics listening", "addr", addr)
	return func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}
}

// dialGithubd dials githubd, which fetches checkouts' commit archives
// (ADR-20261027090010 §3) and writes GitHub deliveries (§8). It is reached the way
// imaged reaches it; deliveries need its local socket.
func dialGithubd(ctx context.Context, getenv func(string) string) (*githubdgrpc.Client, error) {
	githubdTLS, err := wire.LoadClientTLSConfigWithPrefix("githubd_", getenv("FAAS_GITHUBD_TLS_CERT_PATH"),
		getenv("FAAS_GITHUBD_TLS_KEY_PATH"), getenv("FAAS_GITHUBD_TLS_CA_PATH"))
	if err != nil {
		return nil, fmt.Errorf("agentd: githubd client TLS: %w", err)
	}
	target := strings.TrimSpace(getenv("FAAS_GITHUBD_TARGET_URL"))
	if target == "" {
		target = strings.TrimSpace(getenv("FAAS_GITHUBD_SOCKET"))
	}
	if target == "" {
		target = "/run/faas/githubd.sock"
	}
	github, err := githubdgrpc.DialContext(ctx, target, githubdTLS)
	if err != nil {
		return nil, fmt.Errorf("agentd: dial githubd %q: %w", target, err)
	}
	return github, nil
}

// configFromEnv reads the replica's settings. The schedd target is a unix
// socket on a single box, or tcp:// with mTLS between nodes.
func configFromEnv(getenv func(string) string) (agentd.Config, string, *tls.Config, error) {
	host, _ := os.Hostname()
	cfg := agentd.Config{Owner: fmt.Sprintf("%s-%d", host, os.Getpid())}
	if v := strings.TrimSpace(getenv("FAAS_AGENTD_CONCURRENCY")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 256 {
			return agentd.Config{}, "", nil, fmt.Errorf("agentd: FAAS_AGENTD_CONCURRENCY must be 1-256, got %q", v)
		}
		cfg.Concurrency = n
	}
	if v := strings.TrimSpace(getenv("FAAS_AGENTD_LEASE_TTL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 3*time.Second {
			return agentd.Config{}, "", nil, fmt.Errorf("agentd: FAAS_AGENTD_LEASE_TTL must be a duration of at least 3s, got %q", v)
		}
		cfg.LeaseTTL = d
	}
	target := strings.TrimSpace(getenv("FAAS_AGENTD_SCHEDD_TARGET"))
	if target == "" {
		target = "unix:///run/faas/schedd.sock"
	}
	var tlsCfg *tls.Config
	if cert := strings.TrimSpace(getenv("FAAS_AGENTD_SCHEDD_TLS_CERT_PATH")); cert != "" {
		var err error
		tlsCfg, err = wire.LoadClientTLSConfig(cert, getenv("FAAS_AGENTD_SCHEDD_TLS_KEY_PATH"), getenv("FAAS_AGENTD_SCHEDD_TLS_CA_PATH"))
		if err != nil {
			return agentd.Config{}, "", nil, fmt.Errorf("agentd: schedd client TLS: %w", err)
		}
	}
	return cfg, target, tlsCfg, nil
}
