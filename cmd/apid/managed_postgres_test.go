package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/prometheus/client_golang/prometheus"
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
	registry := prometheus.NewRegistry()
	service, reconciler, bindingService, bindingReconciler, usageCollector, err := loadManagedPostgres(pool, getenv, slog.New(slog.NewTextHandler(io.Discard, nil)), registry)
	_ = usageCollector
	if err != nil || service == nil || reconciler == nil || bindingService == nil || bindingReconciler == nil {
		t.Fatalf("configured load = %v, %v, %v, %v, %v", service, reconciler, bindingService, bindingReconciler, err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("managed postgres metrics gather: %v", err)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "apid_managed_postgres_provisioning_enabled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("managed postgres metrics were not registered")
	}
}

func TestManagedPostgresUsageCeilingsFollowPlanStorage(t *testing.T) {
	limits, ok := api.ManagedPostgresLimitsFor(api.PlanHobby)
	if !ok {
		t.Fatal("hobby plan missing")
	}
	got := managedPostgresUsageCeilings(limits)
	want := limits.StorageLimitBytes * int64(limits.DatabasesMax) * int64(31*24*time.Hour/time.Second)
	if got.MaxMonthlyStorageByteSeconds != want {
		t.Fatalf("storage ceiling = %d, want %d", got.MaxMonthlyStorageByteSeconds, want)
	}
	free, _ := api.ManagedPostgresLimitsFor(api.PlanFree)
	if got := managedPostgresUsageCeilings(free); got != (managedpostgres.UsageCeilings{}) {
		t.Fatalf("free ceiling = %+v, want zero", got)
	}
}
