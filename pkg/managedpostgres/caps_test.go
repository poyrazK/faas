// adr: 20261025094915 — compute caps hold on running databases.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlanComputeCaps(t *testing.T) {
	const hour = int64(3600)
	cases := []struct {
		name          string
		ceiling       int64
		ledger        int64
		scopes        []computeCapScope
		wantUsed      int64
		wantRemaining int64
		wantTargets   []int64
		wantApply     []bool
	}{
		{name: "first quota is the ceiling less other use", ceiling: 25 * hour, ledger: 1000,
			scopes:   []computeCapScope{{used: 1200}},
			wantUsed: 1200, wantRemaining: 25*hour - 1200, wantTargets: []int64{25 * hour}, wantApply: []bool{true}},
		{name: "ledger counts a database deleted this month", ceiling: 25 * hour, ledger: 5000,
			scopes:   []computeCapScope{{used: 1000, quota: 25 * hour}},
			wantUsed: 5000, wantRemaining: 25*hour - 5000, wantTargets: []int64{25*hour - 4000}, wantApply: []bool{true}},
		{name: "reaching the ceiling suspends", ceiling: 25 * hour, ledger: 0,
			scopes:   []computeCapScope{{used: 25 * hour, quota: 26 * hour}},
			wantUsed: 25 * hour, wantRemaining: 0, wantTargets: []int64{25 * hour}, wantApply: []bool{true}},
		{name: "a lagging ledger above the ceiling suspends at current use", ceiling: 5 * hour, ledger: 6 * hour,
			scopes:   []computeCapScope{{used: 4 * hour, quota: 5 * hour}},
			wantUsed: 6 * hour, wantRemaining: 0, wantTargets: []int64{4 * hour}, wantApply: []bool{true}},
		{name: "an unused database at the ceiling gets one second, never zero", ceiling: 5 * hour, ledger: 5 * hour,
			scopes:   []computeCapScope{{used: 0}},
			wantUsed: 5 * hour, wantRemaining: 0, wantTargets: []int64{1}, wantApply: []bool{true}},
		{name: "a small tightening waits for the next sweep", ceiling: 75 * hour, ledger: 0,
			scopes:   []computeCapScope{{used: 1000, quota: 75*hour - 100}, {used: 100, quota: 75*hour - 1000}},
			wantUsed: 1100, wantRemaining: 75*hour - 1100, wantTargets: []int64{75*hour - 100, 75*hour - 1000}, wantApply: []bool{false, false}},
		{name: "a large tightening writes", ceiling: 75 * hour, ledger: 0,
			scopes:   []computeCapScope{{used: 10 * hour, quota: 75 * hour}, {used: 20 * hour, quota: 75 * hour}},
			wantUsed: 30 * hour, wantRemaining: 45 * hour, wantTargets: []int64{55 * hour, 65 * hour}, wantApply: []bool{true, true}},
		{name: "a raised ceiling lifts a suspension", ceiling: 50 * hour, ledger: 25 * hour,
			scopes:   []computeCapScope{{used: 25 * hour, quota: 25 * hour}},
			wantUsed: 25 * hour, wantRemaining: 25 * hour, wantTargets: []int64{50 * hour}, wantApply: []bool{true}},
		{name: "a held suspension is not rewritten", ceiling: 25 * hour, ledger: 25 * hour,
			scopes:   []computeCapScope{{used: 25 * hour, quota: 25 * hour}},
			wantUsed: 25 * hour, wantRemaining: 0, wantTargets: []int64{25 * hour}, wantApply: []bool{false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planComputeCaps(tc.ceiling, tc.ledger, tc.scopes)
			if err != nil {
				t.Fatal(err)
			}
			if plan.used != tc.wantUsed || plan.remaining != tc.wantRemaining {
				t.Fatalf("used, remaining = %d, %d; want %d, %d", plan.used, plan.remaining, tc.wantUsed, tc.wantRemaining)
			}
			for i, decision := range plan.decisions {
				if decision.target != tc.wantTargets[i] || decision.apply != tc.wantApply[i] {
					t.Errorf("scope %d: target %d apply %v; want %d %v", i, decision.target, decision.apply, tc.wantTargets[i], tc.wantApply[i])
				}
			}
		})
	}
	if _, err := planComputeCaps(0, 0, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero ceiling: %v", err)
	}
}

func TestParseCapEnforcementMode(t *testing.T) {
	cases := map[string]CapEnforcementMode{"": CapEnforcementShadow, " Shadow ": CapEnforcementShadow, "on": CapEnforcementOn, "OFF": CapEnforcementOff}
	for value, want := range cases {
		if got, err := ParseCapEnforcementMode(value); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParseCapEnforcementMode("enforce"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
}

type capTestProvider struct {
	*fakeProvider
	observed map[string]ComputeCapObservation
	setErr   map[string]error
	set      map[string]int64
}

func (p *capTestProvider) ObserveComputeCap(_ context.Context, id string) (ComputeCapObservation, error) {
	observed, ok := p.observed[id]
	if !ok {
		return ComputeCapObservation{}, ErrNotFound
	}
	return observed, nil
}

func (p *capTestProvider) SetComputeCap(_ context.Context, id string, quota int64) error {
	if err := p.setErr[id]; err != nil {
		return err
	}
	p.set[id] = quota
	return nil
}

// capTestStore serves only the two reads the enforcer makes.
type capTestStore struct {
	UsageStore
	databases []Database
	ledger    map[string]int64
}

func (s *capTestStore) ListUsageDatabases(_ context.Context, after UsageDatabaseCursor, limit int) ([]Database, error) {
	if !after.isZero() {
		return nil, nil
	}
	return s.databases, nil
}

func (s *capTestStore) UsageSnapshot(_ context.Context, accountID string, periodStart time.Time) (UsageSnapshot, error) {
	return UsageSnapshot{PeriodStart: monthStart(periodStart), ComputeUnitSeconds: s.ledger[accountID]}, nil
}

func TestCapEnforcerSweep(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-time.Hour)
	newRun := func(t *testing.T, mode CapEnforcementMode) (*capTestProvider, CapSweepSummary, error) {
		t.Helper()
		provider := &capTestProvider{
			fakeProvider: &fakeProvider{capabilities: testCapabilities()},
			observed: map[string]ComputeCapObservation{
				"proj-a":          {Scope: "a", PeriodStart: month, UsedUnitSeconds: 4000, QuotaUnitSeconds: 0},
				"proj-a/br-rest":  {Scope: "a", PeriodStart: month, UsedUnitSeconds: 4000, QuotaUnitSeconds: 0},
				"proj-b":          {Scope: "b", PeriodStart: month, UsedUnitSeconds: 18000, QuotaUnitSeconds: 90000},
				"proj-c":          {Scope: "c", PeriodStart: month.Add(14 * 24 * time.Hour), UsedUnitSeconds: 10},
				"proj-failing":    {Scope: "f", PeriodStart: month, UsedUnitSeconds: 10},
				"proj-uncapped":   {Scope: "u", PeriodStart: month, UsedUnitSeconds: 10},
				"proj-gone-quota": {Scope: "g", PeriodStart: month},
			},
			setErr: map[string]error{"proj-failing": ErrUnavailable},
			set:    map[string]int64{},
		}
		registry := testRegistry(t, provider, func(config *Config) {
			config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
				StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
				MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
		})
		backend, err := registry.Default("us-east-1")
		if err != nil {
			t.Fatal(err)
		}
		database := func(id, account, resource string, state State) Database {
			return Database{ID: id, AccountID: account, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: resource, State: state}
		}
		gone := database("db-gone", "acct-a", "proj-gone-quota", StateDeleted)
		gone.DeletedAt = &deletedAt
		store := &capTestStore{
			databases: []Database{
				database("db-a", "acct-a", "proj-a", StateReady),
				database("db-a-restore", "acct-a", "proj-a/br-rest", StateReady),
				gone,
				database("db-b", "acct-b", "proj-b", StateReady),
				database("db-c", "acct-c", "proj-c", StateReady),
				database("db-f", "acct-f", "proj-failing", StateReady),
				database("db-u", "acct-u", "proj-uncapped", StateReady),
			},
			// acct-a's ledger still holds the deleted database's 9000 seconds.
			ledger: map[string]int64{"acct-a": 13000, "acct-b": 17000},
		}
		ceilings := map[string]int64{"acct-a": 18000, "acct-b": 18000, "acct-c": 18000, "acct-f": 18000}
		enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
			Mode: mode, Now: func() time.Time { return now },
			Ceiling: func(_ context.Context, accountID string) (int64, error) { return ceilings[accountID], nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := enforcer.Enforce(context.Background())
		return provider, summary, err
	}

	t.Run("on", func(t *testing.T) {
		provider, summary, err := newRun(t, CapEnforcementOn)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Enforce error = %v; want the failing account's write error", err)
		}
		// acct-a: one scope for the database and its restore; ledger 13000
		// exceeds live 4000, so 5000 remain and the quota is 4000 + 5000.
		// acct-b: 18000 used of 18000, so the quota drops to its use.
		want := map[string]int64{"proj-a": 9000, "proj-b": 18000}
		if len(provider.set) != len(want) {
			t.Fatalf("quotas written %v; want %v", provider.set, want)
		}
		for id, quota := range want {
			if provider.set[id] != quota {
				t.Errorf("%s quota %d; want %d", id, provider.set[id], quota)
			}
		}
		if summary.Accounts != 5 || summary.ScopesApplied != 2 || summary.SuspendedAccounts != 1 ||
			summary.PeriodMismatch != 1 || summary.Uncapped != 1 || summary.Failed != 1 {
			t.Fatalf("summary %+v", summary)
		}
	})

	t.Run("shadow writes nothing", func(t *testing.T) {
		provider, summary, err := newRun(t, CapEnforcementShadow)
		if err != nil {
			t.Fatal(err)
		}
		if len(provider.set) != 0 || summary.ScopesShadowed != 3 || summary.ScopesApplied != 0 {
			t.Fatalf("shadow wrote %v, summary %+v", provider.set, summary)
		}
	})

	t.Run("off does nothing", func(t *testing.T) {
		provider, summary, err := newRun(t, CapEnforcementOff)
		if err != nil || len(provider.set) != 0 || summary.Accounts != 0 {
			t.Fatalf("off: %v, %v, %+v", err, provider.set, summary)
		}
	})
}

func TestCapEnforcerOnNeedsTheUsageLedger(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	ceiling := func(context.Context, string) (int64, error) { return 1, nil }
	if _, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementOn, Ceiling: ceiling}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("on without the ledger: %v", err)
	}
	if _, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{Mode: CapEnforcementShadow, Ceiling: ceiling}); err != nil {
		t.Fatalf("shadow without the ledger: %v", err)
	}
}

func TestCapEnforcerSkipsProvidersWithoutQuotas(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := &capTestStore{databases: []Database{{ID: "db", AccountID: "acct", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "p", State: StateReady}}}
	enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{Mode: CapEnforcementShadow, Ceiling: func(context.Context, string) (int64, error) { return 1, nil }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := enforcer.Enforce(context.Background())
	if err != nil || summary.Unsupported != 1 {
		t.Fatalf("summary %+v, %v", summary, err)
	}
}

func TestUsageCollectorRunsTheCapEnforcerAfterEverySweep(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
			MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sweeps := 0
	collector, err := NewUsageCollector(registry, NewMemoryStore(), UsageCollectorOptions{AfterSweep: func(context.Context) {
		sweeps++
		cancel()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := collector.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	if sweeps != 1 {
		t.Fatalf("after-sweep hook ran %d times; want 1", sweeps)
	}
}

// capSubjectTestStore pools by an explicit subject map, as the catalog's
// managed_postgres_usage_subject does for organizations that own resources.
type capSubjectTestStore struct {
	*capTestStore
	subjects map[string]string
}

func (s *capSubjectTestStore) UsageSubjects(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if subject, ok := s.subjects[id]; ok {
			out[id] = subject
		}
	}
	return out, nil
}

func TestCapEnforcerPoolsAnOrganizationAcrossNamespaces(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	provider := &capTestProvider{
		fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		observed: map[string]ComputeCapObservation{
			"proj-1": {Scope: "1", PeriodStart: month, UsedUnitSeconds: 6000},
			"proj-2": {Scope: "2", PeriodStart: month, UsedUnitSeconds: 8000},
		},
		set: map[string]int64{},
	}
	registry := testRegistry(t, provider, nil)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	database := func(id, namespace, resource string) Database {
		return Database{ID: id, AccountID: namespace, OrgID: "org", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: resource, State: StateReady}
	}
	store := &capSubjectTestStore{
		capTestStore: &capTestStore{databases: []Database{database("db-1", "ns-1", "proj-1"), database("db-2", "ns-2", "proj-2")}},
		subjects:     map[string]string{"db-1": "org", "db-2": "org"},
	}
	var asked []string
	enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
		Mode: CapEnforcementShadow, Now: func() time.Time { return now },
		Ceiling: func(_ context.Context, subject string) (int64, error) {
			asked = append(asked, subject)
			return 18000, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := enforcer.Enforce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// One ceiling lookup and one pool for the organization, not one per namespace.
	if len(asked) != 1 || asked[0] != "org" || summary.Accounts != 1 || summary.ScopesShadowed != 2 {
		t.Fatalf("ceilings asked for %v, summary %+v; want one org pool", asked, summary)
	}
}
