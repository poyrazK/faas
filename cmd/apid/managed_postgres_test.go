package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoadManagedPostgresIsDarkWhenUnconfigured(t *testing.T) {
	service, reconciler, bindingService, bindingReconciler, usageCollector, err := loadManagedPostgres(nil, func(string) string { return "" }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = usageCollector
	if err != nil || service != nil || reconciler != nil || bindingService != nil || bindingReconciler != nil {
		t.Fatalf("unconfigured load = %v, %v, %v, %v, %v", service, reconciler, bindingService, bindingReconciler, err)
	}
}

func TestLoadManagedPostgresRegistersNeonDriver(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "managed-postgres.json")
	config := `{
  "default_region": "eu-central-1",
  "defaults": {"eu-central-1": "neon-eu"},
  "max_databases_per_account": 3,
  "provisioning_enabled": false,
  "backends": [{
    "id": "neon-eu",
    "driver": "neon",
    "region": "eu-central-1",
    "namespace": "org-gregale-12345678",
    "settings": {
      "region_id": "aws-eu-central-1",
      "database_name": "gregale",
      "max_storage_bytes": "107374182400",
      "max_restore_window_seconds": "604800"
    },
    "secret_env": {"api-key": "TEST_NEON_API_KEY"}
  }]
}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	databaseConfig, err := pgxpool.ParseConfig("postgres://postgres:postgres@127.0.0.1:1/faas?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	getenv := func(key string) string {
		switch key {
		case "FAAS_MANAGED_POSTGRES_CONFIG":
			return configPath
		case "TEST_NEON_API_KEY":
			return "secret"
		default:
			return ""
		}
	}
	service, reconciler, bindingService, bindingReconciler, usageCollector, err := loadManagedPostgres(pool, getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = usageCollector
	if err != nil || service == nil || reconciler == nil || bindingService == nil || bindingReconciler == nil {
		t.Fatalf("configured load = %v, %v, %v, %v, %v", service, reconciler, bindingService, bindingReconciler, err)
	}
}
