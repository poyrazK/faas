// Command outboundd is the explicit request-aware gateway for configured
// third-party integrations. Workloads opt in by calling /i/{integration_id}/;
// it is not a transparent packet proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() { wire.Daemon("outboundd", run) }

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := LoadConfig(defaultConfigPath())
	if err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("outboundd")
	wire.BootStamps(ctx, "outboundd", ops)
	wire.RegisterDefaultOps(ops)
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "outboundd")
	if err != nil {
		return fmt.Errorf("outboundd: open db: %w", err)
	}
	defer pool.Close()
	configured, err := cfg.Policies(os.Getenv)
	if err != nil {
		return err
	}
	for _, item := range configured {
		if err := outbound.EnsureIntegration(ctx, pool, item.Record); err != nil {
			return fmt.Errorf("outboundd: provision integration %s: %w", item.Record.Policy.ID, err)
		}
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		return err
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		return err
	}
	handler, err := outbound.NewHandler(resolver, backend, nil)
	if err != nil {
		return err
	}
	outboundMetrics, err := outbound.NewMetrics(ops.Registry())
	if err != nil {
		return fmt.Errorf("outboundd: register metrics: %w", err)
	}
	handler.Metrics = outboundMetrics
	handler.MaxBodyBytes = cfg.MaxBodyBytes
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	var metricsServer *http.Server
	if cfg.MetricsAddr != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", ops.Handler())
		wire.ControlMuxLite(metricsMux, nil, nil)
		metricsServer = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metricsMux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		go func() {
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("outboundd: metrics http", "err", err)
			}
		}()
		log.Info("outboundd metrics listening", "addr", cfg.MetricsAddr)
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if metricsServer != nil {
			_ = metricsServer.Shutdown(shutdownCtx)
		}
	}()
	// All startup dependencies (config, Postgres, policy resolution, and the
	// metrics registry) are ready before the serving listener is entered. Keep
	// the daemon-level metric aligned with the /readyz contract; wire.Daemon
	// flips it back to 0 on shutdown or an early return.
	ops.MarkReady("outboundd", true, "")
	log.Info("outbound gateway listening", "addr", cfg.ListenAddr, "integrations", len(configured))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
