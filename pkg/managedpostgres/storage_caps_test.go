// adr: 20261025094915 — the storage pool keeps an account inside its pooled
// storage by limiting each branch (decision 9), in shadow first.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const mib = int64(1 << 20)

func TestStorageWaterLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sizes  []int64
		budget int64
		want   int64
	}{
		{name: "one branch gets its size plus the budget", sizes: []int64{10}, budget: 5, want: 15},
		{name: "no budget stops at the smallest branch", sizes: []int64{30, 10, 20}, budget: 0, want: 10},
		{name: "the budget lifts the smallest branch first", sizes: []int64{10, 20}, budget: 5, want: 15},
		{name: "the budget is shared once branches meet", sizes: []int64{10, 20}, budget: 30, want: 30},
		{name: "equal branches split it", sizes: []int64{10, 10, 10}, budget: 9, want: 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := storageWaterLevel(tc.sizes, tc.budget)
			if got != tc.want {
				t.Fatalf("level %d, want %d", got, tc.want)
			}
			var growth int64
			for _, size := range tc.sizes {
				growth += max(0, got-size)
			}
			if growth > tc.budget {
				t.Fatalf("branches may grow %d past a budget of %d", growth, tc.budget)
			}
		})
	}
}

func TestPlanStorageCaps(t *testing.T) {
	if _, err := planStorageCaps(0, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("a zero pool must be refused, not read as no room", err)
	}
	for _, tc := range []struct {
		name      string
		pool      int64
		scopes    []storageCapScope
		remaining int64
		targets   []int64
		applies   []bool
	}{
		{
			name: "room left: each scope gets its size plus what remains",
			pool: 1024 * mib, remaining: 124 * mib,
			scopes: []storageCapScope{
				{sizes: []int64{300 * mib, 500 * mib}, current: 1024 * mib, specLimit: 1024 * mib},
				{sizes: []int64{100 * mib}, current: 1024 * mib, specLimit: 1024 * mib},
			},
			targets: []int64{424 * mib, 224 * mib}, applies: []bool{true, true},
		},
		{
			name: "plenty of room keeps the spec's limit",
			pool: 1024 * mib, remaining: 924 * mib,
			scopes:  []storageCapScope{{sizes: []int64{100 * mib}, current: 512 * mib, specLimit: 512 * mib}},
			targets: []int64{512 * mib}, applies: []bool{false},
		},
		{
			name: "at the pool every branch stops growing",
			pool: 512 * mib, remaining: 0,
			scopes:  []storageCapScope{{sizes: []int64{700 * mib}, current: 1024 * mib, specLimit: 1024 * mib}},
			targets: []int64{700 * mib}, applies: []bool{true},
		},
		{
			name: "a small move is left alone",
			pool: 1024 * mib, remaining: 524 * mib,
			scopes:  []storageCapScope{{sizes: []int64{500 * mib}, current: 1020 * mib, specLimit: 2048 * mib}},
			targets: []int64{1024 * mib}, applies: []bool{false},
		},
		{
			name: "a scope with no limit is always written",
			pool: 1024 * mib, remaining: 924 * mib,
			scopes:  []storageCapScope{{sizes: []int64{100 * mib}, specLimit: 1024 * mib}},
			targets: []int64{1024 * mib}, applies: []bool{true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planStorageCaps(tc.pool, tc.scopes)
			if err != nil {
				t.Fatal(err)
			}
			if plan.remaining != tc.remaining || len(plan.decisions) != len(tc.targets) {
				t.Fatalf("plan %+v", plan)
			}
			for i, decision := range plan.decisions {
				if decision.target != tc.targets[i] || decision.apply != tc.applies[i] {
					t.Errorf("scope %d: target %d apply %v; want %d %v", i, decision.target, decision.apply, tc.targets[i], tc.applies[i])
				}
			}
		})
	}
}

func TestParseStorageCapEnforcementMode(t *testing.T) {
	for value, want := range map[string]CapEnforcementMode{"": CapEnforcementShadow, " Shadow ": CapEnforcementShadow, "off": CapEnforcementOff, "ON": CapEnforcementOn} {
		if got, err := ParseStorageCapEnforcementMode(value); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParseStorageCapEnforcementMode("enforce"); !errors.Is(err, ErrInvalid) {
		t.Error("an unknown mode was accepted")
	}
}

type storageCapTestProvider struct {
	*capTestProvider
	storage       map[string]StorageCapObservation
	storageSet    map[string]int64
	storageSetErr map[string]error
}

func (p *storageCapTestProvider) ObserveStorageCap(_ context.Context, id string) (StorageCapObservation, error) {
	observed, ok := p.storage[id]
	if !ok {
		return StorageCapObservation{}, ErrUnavailable
	}
	return observed, nil
}

func (p *storageCapTestProvider) SetStorageCap(_ context.Context, id string, limit int64) error {
	if err := p.storageSetErr[id]; err != nil {
		return err
	}
	p.storageSet[id] = limit
	return nil
}

// The storage pass runs beside the compute sweep: shadow writes no limit, on
// writes the limits that changed, and a subject it cannot observe or write
// never fails the compute sweep.
func TestCapEnforcerStoragePool(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	run := func(t *testing.T, mode CapEnforcementMode, setErr map[string]error) (*storageCapTestProvider, *CapMetrics, CapSweepSummary) {
		t.Helper()
		provider := &storageCapTestProvider{
			capTestProvider: &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, observed: map[string]ComputeCapObservation{}, set: map[string]int64{}},
			storage: map[string]StorageCapObservation{
				"proj-a":         {Scope: "a", SizeBytes: 300 * mib, LimitBytes: 1024 * mib},
				"proj-a/br-rest": {Scope: "a", SizeBytes: 500 * mib, LimitBytes: 1024 * mib},
				"proj-a2":        {Scope: "a2", SizeBytes: 100 * mib, LimitBytes: 1024 * mib},
				"proj-b":         {Scope: "b", SizeBytes: 700 * mib, LimitBytes: 512 * mib},
				"proj-u":         {Scope: "u", SizeBytes: 1, LimitBytes: 512 * mib},
			},
			storageSet: map[string]int64{}, storageSetErr: setErr,
		}
		for _, id := range []string{"proj-a", "proj-a/br-rest", "proj-a2", "proj-b", "proj-f", "proj-u"} {
			provider.observed[id] = ComputeCapObservation{Scope: id, PeriodStart: monthStart(now), UsedUnitSeconds: 10, QuotaUnitSeconds: 100}
		}
		registry := testRegistry(t, provider, nil)
		backend, err := registry.Default("us-east-1")
		if err != nil {
			t.Fatal(err)
		}
		database := func(id, account, resource string, spec int64) Database {
			d := Database{ID: id, AccountID: account, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
				ProviderResourceID: resource, State: StateReady, Spec: testSpec()}
			d.Spec.StorageLimitBytes = spec
			return d
		}
		store := &capTestStore{databases: []Database{
			database("db-a", "acct-a", "proj-a", 1024*mib),
			database("db-a-restore", "acct-a", "proj-a/br-rest", 1024*mib),
			database("db-a2", "acct-a", "proj-a2", 1024*mib),
			database("db-b", "acct-b", "proj-b", 512*mib),
			database("db-f", "acct-f", "proj-f", 512*mib),
			database("db-u", "acct-u", "proj-u", 512*mib),
		}}
		pools := map[string]int64{"acct-a": 1024 * mib, "acct-b": 512 * mib, "acct-f": 512 * mib}
		metrics, err := NewCapMetrics(prometheus.NewRegistry(), "apid", CapEnforcementShadow, mode)
		if err != nil {
			t.Fatal(err)
		}
		options := CapEnforcerOptions{
			Mode: CapEnforcementShadow, StorageMode: mode, Now: func() time.Time { return now }, Observe: metrics.Observe,
			Ceiling: func(_ context.Context, subject string) (CapLimits, error) {
				// No compute ceiling: the compute sweep counts every subject uncapped.
				return CapLimits{StoragePool: pools[subject]}, nil
			},
		}
		if mode == CapEnforcementOn {
			if _, err := NewCapEnforcer(registry, store, options); !errors.Is(err, ErrInvalid) {
				t.Fatal("storage enforcement on was accepted without its qualification", err)
			}
			options.StorageQualification = testStorageCapQualification(backend)
		}
		enforcer, err := NewCapEnforcer(registry, store, options)
		if err != nil {
			t.Fatal(err)
		}
		summary, err := enforcer.Enforce(context.Background())
		if err != nil {
			t.Fatalf("Enforce = %v; a storage failure must not fail the sweep", err)
		}
		if summary.Accounts != 4 || summary.Uncapped != 4 || summary.Failed != 0 || len(provider.set) != 0 {
			t.Fatalf("the storage pass changed the compute sweep: %+v", summary)
		}
		return provider, metrics, summary
	}

	// acct-a: 900 MiB of 1024 leaves 124. Its shared project's branches (300
	// and 500 MiB) may grow by 124 together, so the limit is 424 MiB; the
	// other project gets 224 MiB. acct-b is past its pool and already limited
	// to its spec's 512 MiB, which stops it: unchanged.
	t.Run("shadow", func(t *testing.T) {
		provider, metrics, summary := run(t, CapEnforcementShadow, nil)
		if len(provider.storageSet) != 0 || summary.StorageAccounts != 4 || summary.StorageScopesShadowed != 2 ||
			summary.StorageScopesApplied != 0 || summary.StorageScopesUnchanged != 1 || summary.StorageBlockedAccounts != 1 ||
			summary.StorageUncapped != 1 || summary.StorageFailed != 1 || summary.StorageUnsupported != 0 {
			t.Fatalf("shadow wrote %v; summary %+v", provider.storageSet, summary)
		}
		for state, want := range map[string]float64{"enforced": 2, "blocked": 1, "uncapped": 1, "failed": 1} {
			if got := testutil.ToFloat64(metrics.storageAccounts.WithLabelValues(state)); got != want {
				t.Errorf("storage accounts %s = %v, want %v", state, got, want)
			}
		}
		if got := testutil.ToFloat64(metrics.storageScopes.WithLabelValues("shadowed")); got != 2 {
			t.Errorf("shadowed decisions = %v, want 2", got)
		}
	})

	t.Run("on writes the limits that changed", func(t *testing.T) {
		provider, metrics, summary := run(t, CapEnforcementOn, nil)
		want := map[string]int64{"proj-a": 424 * mib, "proj-a2": 224 * mib}
		if len(provider.storageSet) != len(want) || provider.storageSet["proj-a"] != want["proj-a"] || provider.storageSet["proj-a2"] != want["proj-a2"] {
			t.Fatalf("limits written %v; want %v", provider.storageSet, want)
		}
		if summary.StorageScopesApplied != 2 || summary.StorageScopesShadowed != 0 || summary.StorageFailed != 1 {
			t.Fatalf("summary %+v", summary)
		}
		if got := testutil.ToFloat64(metrics.storageScopes.WithLabelValues("applied")); got != 2 {
			t.Errorf("applied decisions = %v, want 2", got)
		}
	})

	t.Run("a failed write fails only its subject's storage pass", func(t *testing.T) {
		provider, _, summary := run(t, CapEnforcementOn, map[string]error{"proj-a2": ErrUnavailable})
		if provider.storageSet["proj-a"] != 424*mib || summary.StorageScopesApplied != 1 || summary.StorageFailed != 2 {
			t.Fatalf("limits written %v; summary %+v", provider.storageSet, summary)
		}
	})
}
