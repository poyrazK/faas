package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"os"
)

func archiveDeletionClient(backend storage.StorageBackend, log *slog.Logger) *logarchive.RoutedClient {
	cfg, err := logarchive.ConfigFromEnv(os.Getenv, log)
	if err != nil {
		log.Warn("logarchive.cleanup_config_failed", "err", err)
		return nil
	}
	path := envOr(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
	if creds, err := logarchive.ReadCredentials(path); err == nil {
		cfg = cfg.WithCredentials(creds)
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Warn("logarchive.cleanup_credentials_failed", "err", err)
	}
	if !cfg.Enabled() {
		return nil
	}
	client, err := logarchive.ClientForConfig(cfg, backend)
	if err != nil {
		log.Warn("logarchive.cleanup_client_failed", "err", err)
		return nil
	}
	return client
}

func startArchiveRetention(ctx context.Context, store state.Store, client *logarchive.RoutedClient, registry prometheus.Registerer, log *slog.Logger) {
	if client == nil {
		return
	}
	catalog, ok := store.(state.RuntimeLogArchiveRetentionStore)
	if !ok {
		log.Warn("logarchive.retention_store_unavailable")
		return
	}
	collector, err := logarchive.NewRetentionCollector(logarchive.RetentionParams{Store: catalog, Archives: client, Registry: registry, Log: log})
	if err != nil {
		log.Warn("logarchive.retention_init_failed", "err", err)
		return
	}
	go collector.Run(ctx)
}
