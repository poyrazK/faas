// adr: 20261028103115 — production provisioning: operator backend pin,
// approval window, usage metering and fleet ceilings.
// adr: 20261031091607 — production allows every plan its database count, and a
// paid reserve keeps Free sign-ups from pausing paying customers.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
		config.MaxDatabasesPerAccount = api.ManagedPostgresPlanDatabasesMax()
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
		"low db limit":     {registry: productionRegistry(t, func(c *Config) { c.MaxDatabasesPerAccount = 3 }), reason: "database_limit_below_plan"},
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
	if got := registry.Fleet(); got != (FleetPolicy{MaxDatabases: 10, MaxMonthlyCostMillicents: 1_000_000, PaidReserve: 2}) || !got.Complete() {
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
	if err := service.admitFleet(ctx, "", false); err != nil {
		t.Fatalf("cost increase under the breaker: %v", err)
	}
	record(month.Add(2*time.Hour), 1)
	if err := service.admitFleet(ctx, "", false); !errors.Is(err, ErrFleetPaused) {
		t.Fatalf("cost increase with the breaker open = %v", err)
	}

	unbounded := testService(t, testRegistry(t, &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}, nil), store)
	if err := unbounded.admitFleet(ctx, "", true); err != nil {
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

func TestProductionRegistryDefaultsToThePlansDatabaseLimit(t *testing.T) {
	environment := func(env string) func(string) string {
		return func(name string) string {
			if name == EnvironmentEnv {
				return env
			}
			return "secret"
		}
	}
	for name, tc := range map[string]struct {
		getenv func(string) string
		want   int
	}{
		"production": {getenv: environment(ProductionEnvironment), want: api.ManagedPostgresPlanDatabasesMax()},
		"staging":    {getenv: environment("staging"), want: 3},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
			registry, err := NewRegistry(Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "primary-a"},
				Backends: []BackendConfig{{ID: "primary-a", Driver: "fake", Region: "us-east-1", Namespace: "provider-account-a",
					Settings: map[string]string{"endpoint": "https://db.example.test"}, SecretEnv: map[string]string{"api-key": "TEST_DATABASE_API_KEY"}}}},
				tc.getenv, map[string]Factory{"fake": func(BackendConfig, func(string) string) (Provider, error) { return provider, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if registry.MaxDatabasesPerAccount != tc.want {
				t.Fatalf("per-account limit = %d, want %d", registry.MaxDatabasesPerAccount, tc.want)
			}
		})
	}
	if api.ManagedPostgresPlanDatabasesMax() < 10 {
		t.Fatalf("Scale sells 10 databases; the plan maximum is %d", api.ManagedPostgresPlanDatabasesMax())
	}
}

func TestFleetPaidReserveStopsFreeSubjectsFirst(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	reserve := 1
	registry := productionRegistry(t, func(c *Config) {
		c.Usage = UsageConfig{}
		c.Fleet = FleetConfig{MaxDatabases: 3, MaxMonthlyCostMillicents: 1_000_000, PaidReserveDatabases: &reserve}
	})
	service := testService(t, registry, store)
	service.freePlan = func(_ context.Context, subject string) (bool, error) { return strings.HasPrefix(subject, "free-"), nil }
	for i, account := range []string{"free-a", "paid-a"} {
		if _, err := service.Create(ctx, CreateRequest{AccountID: account, Name: fmt.Sprintf("db-%d", i), Spec: testSpec()}); err != nil {
			t.Fatalf("database %d: %v", i, err)
		}
	}
	// Two of three are taken: the last one is the paid reserve.
	if _, err := service.Create(ctx, CreateRequest{AccountID: "free-b", Name: "free-late", Spec: testSpec()}); !errors.Is(err, ErrFleetFreePaused) || !errors.Is(err, ErrFleetPaused) {
		t.Fatalf("a Free database inside the paid reserve = %v, want ErrFleetFreePaused", err)
	}
	if _, err := service.Create(ctx, CreateRequest{AccountID: "paid-b", Name: "paid-late", Spec: testSpec()}); err != nil {
		t.Fatalf("a paid database inside the reserve: %v", err)
	}
	if _, err := service.Create(ctx, CreateRequest{AccountID: "paid-c", Name: "paid-full", Spec: testSpec()}); !errors.Is(err, ErrFleetPaused) || errors.Is(err, ErrFleetFreePaused) {
		t.Fatalf("a paid database past the cap = %v, want the plain ErrFleetPaused", err)
	}
	service.freePlan = func(context.Context, string) (bool, error) { return false, errors.New("plan unavailable") }
	if err := service.admitFleet(ctx, "free-a", false); err != nil {
		t.Fatalf("work that adds no database never asks the plan: %v", err)
	}
}

func TestFleetPaidReserveConfig(t *testing.T) {
	for name, tc := range map[string]struct {
		config  FleetConfig
		reserve int
		invalid bool
	}{
		"default share": {config: FleetConfig{MaxDatabases: 1000}, reserve: 1000 * api.ManagedPostgresFleetPaidReservePercent / 100},
		"explicit":      {config: FleetConfig{MaxDatabases: 1000, PaidReserveDatabases: reserveOf(50)}, reserve: 50},
		"explicit none": {config: FleetConfig{MaxDatabases: 1000, PaidReserveDatabases: reserveOf(0)}, reserve: 0},
		"no fleet cap":  {config: FleetConfig{}, reserve: 0},
		"whole fleet":   {config: FleetConfig{MaxDatabases: 10, PaidReserveDatabases: reserveOf(10)}, invalid: true},
		"negative":      {config: FleetConfig{MaxDatabases: 10, PaidReserveDatabases: reserveOf(-1)}, invalid: true},
	} {
		t.Run(name, func(t *testing.T) {
			policy, err := tc.config.policy()
			if tc.invalid {
				if err == nil {
					t.Fatalf("policy = %+v, want an error", policy)
				}
				return
			}
			if err != nil || policy.PaidReserve != tc.reserve {
				t.Fatalf("policy = %+v, %v, want reserve %d", policy, err, tc.reserve)
			}
		})
	}
}

func reserveOf(v int) *int { return &v }
