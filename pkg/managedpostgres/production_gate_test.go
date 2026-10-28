// adr: 20261028103115 — production provisioning: operator backend pin,
// approval window, usage metering and fleet ceilings.
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

func productionRegistry(t *testing.T, mutate func(*Config)) *Registry {
	t.Helper()
	return testRegistry(t, &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}, func(config *Config) {
		config.ProvisioningEnabled = true
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600, StaleAfterSeconds: 7200,
			MaxMonthlyCostMillicents: 1, MaxMonthlyComputeUnitSeconds: 1, MaxMonthlyStorageByteSeconds: 1, MaxMonthlyEgressBytes: 1}
		config.Fleet = FleetConfig{MaxDatabases: 10, MaxMonthlyCostMillicents: 1_000_000}
		if mutate != nil {
			mutate(config)
		}
	})
}

func TestProductionReadinessNeedsPinWindowMeteringAndFleetCeilings(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	registry := productionRegistry(t, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	valid := ProductionApproval{BackendID: backend.ID, Fingerprint: backend.Fingerprint, ApprovedAt: now.Add(-time.Hour), Until: now.Add(30 * 24 * time.Hour)}
	if readiness := registry.ProductionReadiness(valid, now); !readiness.Ready {
		t.Fatalf("valid approval refused: %v", readiness.Reasons)
	}
	for name, tc := range map[string]struct {
		registry *Registry
		approval func(ProductionApproval) ProductionApproval
		reason   string
	}{
		"provisioning off": {registry: productionRegistry(t, func(c *Config) { c.ProvisioningEnabled = false }), reason: "provisioning_disabled"},
		"metering off":     {registry: productionRegistry(t, func(c *Config) { c.Usage = UsageConfig{} }), reason: "usage_policy_disabled"},
		"no fleet cap":     {registry: productionRegistry(t, func(c *Config) { c.Fleet.MaxDatabases = 0 }), reason: "fleet_policy_incomplete"},
		"no spend breaker": {registry: productionRegistry(t, func(c *Config) { c.Fleet.MaxMonthlyCostMillicents = 0 }), reason: "fleet_policy_incomplete"},
		"other backend":    {approval: func(a ProductionApproval) ProductionApproval { a.BackendID = "primary-b"; return a }, reason: "backend_mismatch"},
		"changed config":   {approval: func(a ProductionApproval) ProductionApproval { a.Fingerprint = "0" + a.Fingerprint[1:]; return a }, reason: "backend_fingerprint_mismatch"},
		"no approval":      {approval: func(ProductionApproval) ProductionApproval { return ProductionApproval{} }, reason: "backend_mismatch"},
		"no window":        {approval: func(a ProductionApproval) ProductionApproval { a.Until = time.Time{}; return a }, reason: "approval_missing"},
		"window too long": {approval: func(a ProductionApproval) ProductionApproval {
			a.Until = a.ApprovedAt.Add(api.ManagedPostgresProductionApprovalMaxWindow + time.Second)
			return a
		}, reason: "approval_window_invalid"},
		"window backwards": {approval: func(a ProductionApproval) ProductionApproval { a.Until = a.ApprovedAt; return a }, reason: "approval_window_invalid"},
		"not started": {approval: func(a ProductionApproval) ProductionApproval {
			a.ApprovedAt, a.Until = now.Add(time.Hour), now.Add(48*time.Hour)
			return a
		}, reason: "approval_not_started"},
		"expired": {approval: func(a ProductionApproval) ProductionApproval {
			a.ApprovedAt, a.Until = now.Add(-48*time.Hour), now
			return a
		}, reason: "approval_expired"},
		"two default regions": {registry: productionRegistry(t, func(c *Config) {
			c.Backends = append(c.Backends, BackendConfig{ID: "primary-b", Driver: "fake", Region: "eu-west-1", Namespace: "provider-account-b"})
			c.Defaults["eu-west-1"] = "primary-b"
		}), reason: "default_backend_count_invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			r, approval := registry, valid
			if tc.registry != nil {
				r = tc.registry
			}
			if tc.approval != nil {
				approval = tc.approval(valid)
			}
			readiness := r.ProductionReadiness(approval, now)
			if readiness.Ready || !slices.Contains(readiness.Reasons, tc.reason) {
				t.Fatalf("readiness = %+v, want refusal %q", readiness, tc.reason)
			}
		})
	}
}

func TestProvisioningGateDecidesProductionByApprovalAndStagingByQualification(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	registry := productionRegistry(t, nil)
	backend, _ := registry.Default("us-east-1")
	env := map[string]string{
		EnvironmentEnv:           "production",
		ProductionBackendEnv:     backend.ID,
		ProductionFingerprintEnv: backend.Fingerprint,
		ProductionApprovedAtEnv:  now.Add(-time.Hour).Format(time.RFC3339),
		ProductionUntilEnv:       now.Add(14 * 24 * time.Hour).Format(time.RFC3339),
		// A staging canary list never narrows or opens production.
		CanaryAccountsEnv: "someone-else",
	}
	getenv := func(name string) string { return env[name] }
	clock := now
	gate := NewProvisioningGate(registry, getenv, func() time.Time { return clock })
	if !gate() {
		t.Fatal("an approved production backend must open provisioning")
	}
	clock = now.Add(14 * 24 * time.Hour)
	if gate() {
		t.Fatal("provisioning must close when the approval ends")
	}
	env[EnvironmentEnv] = "staging"
	if NewProvisioningGate(registry, getenv, func() time.Time { return now })() {
		t.Fatal("staging must keep requiring its qualification artifact")
	}
	env[EnvironmentEnv] = ""
	if NewProvisioningGate(registry, getenv, func() time.Time { return now })() {
		t.Fatal("an unnamed environment must stay closed")
	}
	if ProductionEnvironmentActive(getenv) || !ProductionEnvironmentActive(func(string) string { return " Production " }) {
		t.Fatal("production environment detection")
	}
}

func TestFleetConfigBoundsOperatorCeilings(t *testing.T) {
	for _, fleet := range []FleetConfig{
		{MaxDatabases: -1}, {MaxMonthlyCostMillicents: -1},
		{MaxDatabases: api.ManagedPostgresFleetDatabasesCeiling + 1},
		{MaxMonthlyCostMillicents: api.ManagedPostgresFleetMonthlyCostCeilingMillicents + 1},
	} {
		config := Config{Fleet: fleet}
		if _, err := NewRegistry(config, func(string) string { return "" }, nil); err == nil {
			t.Fatalf("fleet %+v accepted", fleet)
		}
	}
	registry := productionRegistry(t, nil)
	if got := registry.Fleet(); got != (FleetPolicy{MaxDatabases: 10, MaxMonthlyCostMillicents: 1_000_000}) || !got.Complete() {
		t.Fatalf("fleet policy = %+v", got)
	}
	if (FleetPolicy{MaxDatabases: 1}).Complete() || (*Registry)(nil).Fleet().Complete() {
		t.Fatal("an incomplete fleet policy reported complete")
	}
}

func TestFleetCeilingsPauseNewDatabasesAndCostIncreases(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	registry := productionRegistry(t, func(c *Config) {
		c.Usage = UsageConfig{}
		c.Fleet = FleetConfig{MaxDatabases: 2, MaxMonthlyCostMillicents: 500}
	})
	service := testService(t, registry, store)
	first, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "one", Spec: testSpec()})
	if err != nil {
		t.Fatalf("first database: %v", err)
	}
	if _, err := service.Create(ctx, CreateRequest{AccountID: "account-b", Name: "two", Spec: testSpec()}); err != nil {
		t.Fatalf("second database: %v", err)
	}
	if _, err := service.Create(ctx, CreateRequest{AccountID: "account-c", Name: "three", Spec: testSpec()}); !errors.Is(err, ErrFleetPaused) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a database past the fleet cap = %v, want ErrFleetPaused", err)
	}
	if again, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "one", Spec: testSpec()}); err != nil || again.ID != first.ID {
		t.Fatalf("an idempotent replay adds no database and must pass the cap: %v", err)
	}
	totals, policy, err := service.FleetStatus(ctx)
	if err != nil || totals.Databases != 2 || policy.MaxDatabases != 2 {
		t.Fatalf("fleet status = %+v %+v %v", totals, policy, err)
	}

	// The spend breaker counts only this month's ledger.
	month := monthStart(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	record := func(from time.Time, cost int64) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.usage[usageKey{databaseID: first.ID, from: from, to: from.Add(time.Hour), meter: MeterComputeUnitSeconds}] = UsageRecord{
			AccountID: first.AccountID, DatabaseID: first.ID, WindowFrom: from, WindowTo: from.Add(time.Hour), Meter: MeterComputeUnitSeconds, CostMillicents: cost}
	}
	record(month.Add(-time.Hour), 10_000)
	record(month.Add(time.Hour), 499)
	if totals, _, _ := service.FleetStatus(ctx); totals.CostMillicents != 499 {
		t.Fatalf("month-to-date cost = %d, want 499", totals.CostMillicents)
	}
	if err := service.admitFleet(ctx, false); err != nil {
		t.Fatalf("cost increase under the breaker: %v", err)
	}
	record(month.Add(2*time.Hour), 1)
	if err := service.admitFleet(ctx, false); !errors.Is(err, ErrFleetPaused) {
		t.Fatalf("cost increase with the breaker open = %v", err)
	}

	unbounded := testService(t, testRegistry(t, &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}, nil), store)
	if err := unbounded.admitFleet(ctx, true); err != nil {
		t.Fatalf("no fleet policy must admit: %v", err)
	}
}

func TestMetricsExposeFleetCeilingsAndProductionApproval(t *testing.T) {
	metrics, err := NewMetrics(prometheus.NewRegistry(), "apid", true)
	if err != nil {
		t.Fatal(err)
	}
	metrics.ObserveFleet(FleetTotals{Databases: 7, CostMillicents: 1234}, FleetPolicy{MaxDatabases: 10, MaxMonthlyCostMillicents: 5000})
	until := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	metrics.SetProductionApprovalUntil(until)
	for name, tc := range map[string]struct {
		collector prometheus.Collector
		want      float64
	}{
		"databases":     {metrics.fleetDatabases, 7},
		"cost":          {metrics.fleetCost, 1234},
		"max databases": {metrics.fleetMaxDatabases, 10},
		"max cost":      {metrics.fleetMaxCost, 5000},
		"approval":      {metrics.productionApproval, float64(until.Unix())},
	} {
		if got := testutil.ToFloat64(tc.collector); got != tc.want {
			t.Fatalf("%s = %v, want %v", name, got, tc.want)
		}
	}
	metrics.SetProductionApprovalUntil(time.Time{})
	if got := testutil.ToFloat64(metrics.productionApproval); got != 0 {
		t.Fatalf("no approval = %v", got)
	}
	var none *Metrics
	none.ObserveFleet(FleetTotals{}, FleetPolicy{})
	none.SetProductionApprovalUntil(until)
}
