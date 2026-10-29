package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestLoadManagedPostgresIsDarkWhenUnconfigured(t *testing.T) {
	service, reconciler, bindingService, bindingReconciler, usageCollector, healthCollector, err := apidserver.LoadManagedPostgres(nil, func(string) string { return "" }, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil || service != nil || reconciler != nil || bindingService != nil || bindingReconciler != nil || usageCollector != nil || healthCollector != nil {
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
	service, reconciler, bindingService, bindingReconciler, usageCollector, healthCollector, err := apidserver.LoadManagedPostgres(pool, getenv, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, registry)
	if err != nil || service == nil || reconciler == nil || bindingService == nil || bindingReconciler == nil || usageCollector == nil || healthCollector == nil {
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

// adr: 1350 — storage is pooled per account and compute units are a monthly
// ceiling until managed PostgreSQL is invoiced.
func TestManagedPostgresUsageCeilingsFollowPlanStorage(t *testing.T) {
	month := int64(31 * 24 * time.Hour / time.Second)
	for _, plan := range api.Plans {
		limits, ok := api.ManagedPostgresLimitsFor(plan)
		if !ok {
			t.Fatalf("%s plan missing", plan)
		}
		for _, overage := range []bool{false, true} {
			got := apidserver.ManagedPostgresUsageCeilings(limits, overage)
			want := managedpostgres.UsageCeilings{
				MaxMonthlyStorageByteSeconds: limits.StorageLimitBytes * month,
				MaxMonthlyComputeUnitSeconds: limits.MonthlyComputeUnitSeconds,
			}
			// adr: 20261025094915 — overage raises only the compute ceiling, to 2x.
			if overage {
				want.MaxMonthlyComputeUnitSeconds *= api.ManagedPostgresOverageCeilingMultiplier
			}
			if got != want {
				t.Fatalf("%s overage=%v ceiling = %+v, want the pooled %+v", plan, overage, got, want)
			}
		}
	}
	if got := apidserver.ManagedPostgresUsageCeilings(api.ManagedPostgresPlanLimits{}, true); got != (managedpostgres.UsageCeilings{}) {
		t.Fatalf("no-database ceiling = %+v, want zero", got)
	}
}
