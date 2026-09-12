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
	handler.MaxBodyBytes = cfg.MaxBodyBytes
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Info("outbound gateway listening", "addr", cfg.ListenAddr, "integrations", len(configured))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
