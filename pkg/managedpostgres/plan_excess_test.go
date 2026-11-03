// adr: 20261102125208 — databases past a lowered plan are suspended, not deleted.
package managedpostgres

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParsePlanExcessEnforcementMode(t *testing.T) {
	for value, want := range map[string]CapEnforcementMode{"": CapEnforcementShadow, " ON ": CapEnforcementOn, "off": CapEnforcementOff, "shadow": CapEnforcementShadow} {
		if got, err := ParsePlanExcessEnforcementMode(value); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParsePlanExcessEnforcementMode("suspend"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestPlanExcess(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	free, _ := api.ManagedPostgresLimitsFor(api.PlanFree)
	pro, _ := api.ManagedPostgresLimitsFor(api.PlanPro)
	database := func(id string, age int, class ServiceClass) Database {
		return Database{ID: id, ProviderResourceID: "proj-" + id, State: StateReady, Spec: Spec{Class: class},
			CreatedAt: start.Add(time.Duration(age) * time.Hour)}
	}
	slot := database("slot", 0, ClassDevelopment)
	slot.RestoreSlotUntil = start.Add(7 * 24 * time.Hour)
	deleting := database("deleting", 0, ClassDevelopment)
	deleting.State = StateDeleting
	pending := database("pending", 0, ClassDevelopment)
	pending.ProviderResourceID = ""

	tests := []struct {
		name      string
		databases []Database
		limits    api.ManagedPostgresPlanLimits
		want      map[string]string
	}{
		{name: "an unknown plan suspends nothing",
			databases: []Database{database("a", 1, ClassProduction), database("b", 2, ClassProduction)}},
		{name: "free keeps the oldest database",
			databases: []Database{database("c", 3, ClassDevelopment), database("a", 1, ClassDevelopment), database("b", 2, ClassBurstable)},
			limits:    free,
			want:      map[string]string{"b": PlanExcessDatabaseLimit, "c": PlanExcessDatabaseLimit}},
		{name: "a class the plan lacks is past it, and still counts toward the limit",
			databases: []Database{database("a", 1, ClassProduction), database("b", 2, ClassBurstable), database("c", 3, ClassDevelopment), database("d", 4, ClassDevelopment)},
			limits:    pro,
			want:      map[string]string{"a": PlanExcessServiceClass, "d": PlanExcessDatabaseLimit}},
		{name: "the restore slot, a deleting database and one not yet provisioned are not ranked",
			databases: []Database{slot, deleting, pending, database("a", 1, ClassDevelopment), database("b", 2, ClassDevelopment)},
			limits:    free,
			want:      map[string]string{"b": PlanExcessDatabaseLimit}},
		{name: "the same creation time ranks by ID",
			databases: []Database{database("y", 1, ClassDevelopment), database("x", 1, ClassDevelopment)},
			limits:    free,
			want:      map[string]string{"y": PlanExcessDatabaseLimit}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := PlanExcess(test.databases, test.limits)
			if len(got) != len(test.want) || !maps.Equal(got, test.want) {
				t.Fatalf("PlanExcess = %v; want %v", got, test.want)
			}
		})
	}
}

func TestCapEnforcerSuspendsDatabasesPastThePlan(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	free, _ := api.ManagedPostgresLimitsFor(api.PlanFree)
	pro, _ := api.ManagedPostgresLimitsFor(api.PlanPro)
	type run struct {
		mode, excess CapEnforcementMode
		plan         api.ManagedPostgresPlanLimits
		quota2       int64
		class2       ServiceClass
	}
	sweep := func(t *testing.T, r run) (map[string]int64, CapSweepSummary) {
		t.Helper()
		provider := &capTestProvider{
			fakeProvider: &fakeProvider{capabilities: testCapabilities()},
			observed: map[string]ComputeCapObservation{
				// db-1 and its restore share one project, so one scope.
				"proj-1":    {Scope: "1", PeriodStart: month, UsedUnitSeconds: 100},
				"proj-1/br": {Scope: "1", PeriodStart: month, UsedUnitSeconds: 100},
				"proj-2":    {Scope: "2", PeriodStart: month, UsedUnitSeconds: 50, QuotaUnitSeconds: r.quota2},
			},
			set: map[string]int64{},
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
		database := func(id, resource string, age int, class ServiceClass) Database {
			return Database{ID: id, AccountID: "acct", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
				ProviderResourceID: resource, State: StateReady, Spec: Spec{Class: class},
				CreatedAt: month.Add(time.Duration(age) * time.Hour)}
		}
		class2 := r.class2
		if class2 == "" {
			class2 = ClassDevelopment
		}
		store := &capTestStore{databases: []Database{
			database("db-1", "proj-1", 1, ClassDevelopment),
			database("db-2", "proj-2", 2, class2),
			database("db-1-restore", "proj-1/br", 3, ClassDevelopment),
		}}
		enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
			Mode: r.mode, PlanExcessMode: r.excess, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
			Ceiling: func(context.Context, string) (CapLimits, error) {
				return CapLimits{Allowance: 18000, Ceiling: 18000, Entitlement: r.plan}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := enforcer.Enforce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return provider.set, summary
	}
	// 18000 − (100 + 50) leaves 17850 for each scope above its own use.
	const scope1, scope2 = 100 + 17850, 50 + 17850

	t.Run("on suspends a scope past the plan and leaves a shared one running", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementOn, excess: CapEnforcementOn, plan: free})
		if want := map[string]int64{"proj-1": scope1, "proj-2": 50}; !maps.Equal(set, want) {
			t.Fatalf("quotas %v; want %v", set, want)
		}
		if summary.PlanExcessDatabases != 2 || summary.PlanExcessScopesSuspended != 1 || summary.PlanExcessScopesShared != 1 {
			t.Fatalf("summary %+v", summary)
		}
	})
	t.Run("a class the plan lacks is suspended", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementOn, excess: CapEnforcementOn, plan: pro, class2: ClassProduction})
		if set["proj-2"] != 50 || summary.PlanExcessDatabases != 1 || summary.PlanExcessScopesSuspended != 1 {
			t.Fatalf("quotas %v, summary %+v", set, summary)
		}
	})
	t.Run("an upgrade lifts the suspension", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementOn, excess: CapEnforcementOn, plan: pro, quota2: 50})
		if set["proj-2"] != scope2 || summary.PlanExcessDatabases != 0 {
			t.Fatalf("quotas %v, summary %+v", set, summary)
		}
	})
	t.Run("shadow only logs", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementOn, excess: CapEnforcementShadow, plan: free})
		if set["proj-2"] != scope2 || summary.PlanExcessScopesShadowed != 1 || summary.PlanExcessScopesSuspended != 0 {
			t.Fatalf("quotas %v, summary %+v", set, summary)
		}
	})
	t.Run("on writes nothing while the compute cap is in shadow", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementShadow, excess: CapEnforcementOn, plan: free})
		if len(set) != 0 || summary.PlanExcessScopesShadowed != 1 || summary.PlanExcessScopesSuspended != 0 {
			t.Fatalf("quotas %v, summary %+v", set, summary)
		}
	})
	t.Run("off does not look", func(t *testing.T) {
		set, summary := sweep(t, run{mode: CapEnforcementOn, excess: CapEnforcementOff, plan: free})
		if set["proj-2"] != scope2 || summary.PlanExcessDatabases != 0 {
			t.Fatalf("quotas %v, summary %+v", set, summary)
		}
	})
}

func TestNewCapEnforcerRefusesAnUnknownPlanExcessMode(t *testing.T) {
	registry := testRegistry(t, &capTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}, nil)
	_, err := NewCapEnforcer(registry, &capTestStore{}, CapEnforcerOptions{
		Mode: CapEnforcementShadow, PlanExcessMode: "suspend",
		Ceiling: func(context.Context, string) (CapLimits, error) { return CapLimits{}, nil },
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewCapEnforcer = %v; want ErrInvalid", err)
	}
}

// planExcessNoticeTestStore keeps the sweep's notices beside the two reads
// capTestStore serves.
type planExcessNoticeTestStore struct {
	*capTestStore
	notices map[string]PlanExcessNotice
	mailed  map[string]bool
}

func (s *planExcessNoticeTestStore) SyncPlanExcessNotices(_ context.Context, databaseIDs []string, suspended map[string]string, at time.Time) (int, error) {
	for _, id := range databaseIDs {
		if _, ok := suspended[id]; !ok {
			delete(s.notices, id)
			delete(s.mailed, id)
		}
	}
	recorded := 0
	for id, reason := range suspended {
		if _, ok := s.notices[id]; !ok {
			s.notices[id] = PlanExcessNotice{DatabaseID: id, AccountID: "acct", DatabaseName: id, Reason: reason, RecordedAt: at}
			recorded++
		}
	}
	return recorded, nil
}

func (s *planExcessNoticeTestStore) UnmailedPlanExcessNotices(_ context.Context, since time.Time, _ int) ([]PlanExcessNotice, error) {
	var out []PlanExcessNotice
	for id, n := range s.notices {
		if !s.mailed[id] && n.RecordedAt.After(since) {
			out = append(out, n)
		}
	}
	return out, nil
}

// ADR-20261102125208 step 3: the sweep records a notice for each database it
// suspends, mails it once, and drops it once the subject fits again, so a
// later suspension is mailed again.
func TestCapEnforcerNoticesDatabasesPastThePlan(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	free, _ := api.ManagedPostgresLimitsFor(api.PlanFree)
	pro, _ := api.ManagedPostgresLimitsFor(api.PlanPro)
	provider := &capTestProvider{
		fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		observed: map[string]ComputeCapObservation{
			"proj-1": {Scope: "1", PeriodStart: month, UsedUnitSeconds: 100},
			"proj-2": {Scope: "2", PeriodStart: month, UsedUnitSeconds: 50},
		},
		set: map[string]int64{},
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
	database := func(id, resource string, age int) Database {
		return Database{ID: id, AccountID: "acct", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
			ProviderResourceID: resource, State: StateReady, Spec: Spec{Class: ClassDevelopment},
			CreatedAt: month.Add(time.Duration(age) * time.Hour)}
	}
	store := &planExcessNoticeTestStore{capTestStore: &capTestStore{databases: []Database{database("db-1", "proj-1", 1), database("db-2", "proj-2", 2)}},
		notices: map[string]PlanExcessNotice{}, mailed: map[string]bool{}}
	plan := free
	var mailed []PlanExcessNotice
	notify := func(_ context.Context, n PlanExcessNotice) error {
		mailed = append(mailed, n)
		store.mailed[n.DatabaseID] = true
		return nil
	}
	sweep := func(mode CapEnforcementMode) CapSweepSummary {
		t.Helper()
		enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
			Mode: CapEnforcementOn, PlanExcessMode: mode, NotifyPlanExcess: notify, Now: func() time.Time { return now },
			Qualification: testCapQualification(backend),
			Ceiling: func(context.Context, string) (CapLimits, error) {
				return CapLimits{Allowance: 18000, Ceiling: 18000, Entitlement: plan}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := enforcer.Enforce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}

	if summary := sweep(CapEnforcementShadow); summary.PlanExcessNoticed != 0 || len(store.notices) != 0 || len(mailed) != 0 {
		t.Fatalf("shadow noticed %+v, mailed %v; want nothing", store.notices, mailed)
	}
	summary := sweep(CapEnforcementOn)
	if summary.PlanExcessNoticed != 1 || len(mailed) != 1 || mailed[0].DatabaseID != "db-2" || mailed[0].Reason != PlanExcessDatabaseLimit {
		t.Fatalf("first suspension: summary %+v, mailed %+v; want db-2 noticed and mailed once", summary, mailed)
	}
	if summary := sweep(CapEnforcementOn); summary.PlanExcessNoticed != 0 || len(mailed) != 1 {
		t.Fatalf("second sweep: summary %+v, mailed %d; want nothing new", summary, len(mailed))
	}
	plan = pro
	if sweep(CapEnforcementOn); len(store.notices) != 0 {
		t.Fatalf("after the upgrade notices = %+v; want none", store.notices)
	}
	plan = free
	if sweep(CapEnforcementOn); len(mailed) != 2 {
		t.Fatalf("a second suspension mailed %d times in all; want 2", len(mailed))
	}
}

func TestMemoryStorePlanExcessNotices(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.databases["db-1"] = Database{ID: "db-1", AccountID: "acct", Name: "orders", State: StateReady, Spec: Spec{Class: ClassDevelopment}}
	store.databases["db-2"] = Database{ID: "db-2", AccountID: "acct", Name: "events", State: StateReady, Spec: Spec{Class: ClassProduction}}
	ids := []string{"db-1", "db-2"}
	for i, want := range []int{1, 0} {
		if n, err := store.SyncPlanExcessNotices(ctx, ids, map[string]string{"db-2": PlanExcessServiceClass}, at); err != nil || n != want {
			t.Fatalf("sync %d = %d, %v; want %d", i, n, err, want)
		}
	}
	if _, err := store.SyncPlanExcessNotices(ctx, ids, map[string]string{"db-2": "storage"}, at); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown reason = %v; want ErrInvalid", err)
	}
	notices, err := store.UnmailedPlanExcessNotices(ctx, at.Add(-time.Hour), 10)
	if err != nil || len(notices) != 1 || notices[0].DatabaseName != "events" || notices[0].ServiceClass != ClassProduction || notices[0].AccountID != "acct" {
		t.Fatalf("unmailed = %+v, %v", notices, err)
	}
	store.markPlanExcessNoticeMailed("db-2")
	if notices, _ := store.UnmailedPlanExcessNotices(ctx, at.Add(-time.Hour), 10); len(notices) != 0 {
		t.Fatalf("a mailed notice is listed: %+v", notices)
	}
	if n, err := store.SyncPlanExcessNotices(ctx, ids, nil, at); err != nil || n != 0 || len(store.planExcessNotices) != 0 {
		t.Fatalf("lifting kept %+v, %v", store.planExcessNotices, err)
	}
}
