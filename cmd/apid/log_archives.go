package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/storage"
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
