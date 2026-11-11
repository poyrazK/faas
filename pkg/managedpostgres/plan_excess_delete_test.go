// adr: 20261110184700 — a database suspended past its plan for 30 days has
// its delete scheduled through the delete grace period.
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// planExcessSinceTestStore adds the deletion clock to the notice test store.
type planExcessSinceTestStore struct {
	*planExcessNoticeTestStore
}

func (s planExcessSinceTestStore) PlanExcessSince(_ context.Context, ids []string) (map[string]time.Time, error) {
	since := map[string]time.Time{}
	for _, id := range ids {
		if n, ok := s.notices[id]; ok {
			since[id] = n.RecordedAt
		}
	}
	return since, nil
}

func TestCapEnforcerDeletesDatabasesPastThePlanAfter30Days(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	hobby, _ := api.ManagedPostgresLimitsFor(api.PlanHobby)
	pro, _ := api.ManagedPostgresLimitsFor(api.PlanPro)
	free, _ := api.ManagedPostgresLimitsFor(api.PlanFree)
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

	type run struct {
		now      time.Time
		mode     CapEnforcementMode
		plan     api.ManagedPostgresPlanLimits
		inactive bool
		pending  bool
		fail     error
	}
	var mailed []PlanExcessNotice
	fixture := func() (planExcessSinceTestStore, func(run) (CapSweepSummary, []string)) {
		mailed = nil
		store := planExcessSinceTestStore{&planExcessNoticeTestStore{
			capTestStore: &capTestStore{databases: []Database{database("db-1", "proj-1", 1), database("db-2", "proj-2", 2)}},
			notices:      map[string]PlanExcessNotice{}, mailed: map[string]bool{}}}
		return store, func(r run) (CapSweepSummary, []string) {
			t.Helper()
			period := time.Date(r.now.Year(), r.now.Month(), 1, 0, 0, 0, 0, time.UTC)
			for resource, observed := range provider.observed {
				observed.PeriodStart = period
				provider.observed[resource] = observed
			}
			var deleted []string
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, PlanExcessMode: CapEnforcementOn, PlanExcessDeleteMode: r.mode,
				DeletePlanExcess: func(_ context.Context, d Database) (bool, error) {
					deleted = append(deleted, d.ID)
					return r.pending, r.fail
				},
				NotifyPlanExcess: func(_ context.Context, n PlanExcessNotice) error {
					mailed = append(mailed, n)
					store.mailed[n.DatabaseID] = true
					return nil
				},
				Now: func() time.Time { return r.now }, Qualification: testCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Allowance: 18000, Ceiling: 18000, Entitlement: r.plan, Inactive: r.inactive}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(deleted)
			return summary, deleted
		}
	}
	due := start.Add(api.ManagedPostgresPlanExcessDeleteAfter)

	t.Run("the clock starts at the suspension and runs 30 days", func(t *testing.T) {
		_, sweep := fixture()
		if summary, deleted := sweep(run{now: start, mode: CapEnforcementOn, plan: hobby}); summary.PlanExcessDeleteDue != 0 || len(deleted) != 0 {
			t.Fatalf("first suspension: summary %+v, deleted %v", summary, deleted)
		}
		if len(mailed) != 1 || !mailed[0].DeleteAt.Equal(due) {
			t.Fatalf("mailed %+v; want one notice naming the deletion at %s", mailed, due)
		}
		if summary, deleted := sweep(run{now: due.Add(-time.Second), mode: CapEnforcementOn, plan: hobby}); summary.PlanExcessDeleteDue != 0 || len(deleted) != 0 {
			t.Fatalf("a second early: summary %+v, deleted %v", summary, deleted)
		}
		summary, deleted := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby})
		if !slices.Equal(deleted, []string{"db-2"}) || summary.PlanExcessDeleteDue != 1 || summary.PlanExcessDeleteScheduled != 1 {
			t.Fatalf("at 30 days: summary %+v, deleted %v; want db-2 scheduled", summary, deleted)
		}
	})
	t.Run("shadow only logs", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementShadow, plan: hobby})
		if len(mailed) != 1 || !mailed[0].DeleteAt.IsZero() {
			t.Fatalf("mailed %+v; want no deletion date in shadow", mailed)
		}
		summary, deleted := sweep(run{now: due, mode: CapEnforcementShadow, plan: hobby})
		if len(deleted) != 0 || summary.PlanExcessDeleteShadowed != 1 || summary.PlanExcessDeleteMode != CapEnforcementShadow {
			t.Fatalf("summary %+v, deleted %v", summary, deleted)
		}
	})
	t.Run("off does not look", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOff, plan: hobby})
		if summary, deleted := sweep(run{now: due, mode: CapEnforcementOff, plan: hobby}); len(deleted) != 0 || summary.PlanExcessDeleteDue != 0 {
			t.Fatalf("summary %+v, deleted %v", summary, deleted)
		}
	})
	t.Run("an upgrade stops the clock", func(t *testing.T) {
		store, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: hobby})
		sweep(run{now: start.Add(24 * time.Hour), mode: CapEnforcementOn, plan: pro})
		if len(store.notices) != 0 {
			t.Fatalf("notices after the upgrade: %+v", store.notices)
		}
		if summary, deleted := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby}); len(deleted) != 0 || summary.PlanExcessDeleteDue != 0 {
			t.Fatalf("a new suspension restarted the clock, yet: summary %+v, deleted %v", summary, deleted)
		}
	})
	t.Run("free deletes every database", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: free})
		if _, deleted := sweep(run{now: due, mode: CapEnforcementOn, plan: free}); !slices.Equal(deleted, []string{"db-1", "db-2"}) {
			t.Fatalf("deleted %v; want both", deleted)
		}
	})
	t.Run("an inactive subject is left to its own lifecycle", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: hobby, inactive: true})
		if summary, deleted := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby, inactive: true}); len(deleted) != 0 || summary.PlanExcessDeleteDue != 0 {
			t.Fatalf("summary %+v, deleted %v", summary, deleted)
		}
	})
	t.Run("bindings still going wait, and a failure is counted", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: hobby})
		if summary, _ := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby, pending: true}); summary.PlanExcessDeleteWaiting != 1 {
			t.Fatalf("pending: summary %+v", summary)
		}
		if summary, _ := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby, fail: errors.New("provider down")}); summary.PlanExcessDeleteFailed != 1 {
			t.Fatalf("failure: summary %+v", summary)
		}
	})
}

func TestParsePlanExcessDeleteMode(t *testing.T) {
	for in, want := range map[string]CapEnforcementMode{"": CapEnforcementShadow, "off": CapEnforcementOff, " ON ": CapEnforcementOn, "shadow": CapEnforcementShadow} {
		if got, err := ParsePlanExcessDeleteMode(in); err != nil || got != want {
			t.Fatalf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParsePlanExcessDeleteMode("delete"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestMemoryStorePlanExcessSince(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.databases["db-1"] = Database{ID: "db-1", AccountID: "acct", State: StateReady}
	store.databases["db-2"] = Database{ID: "db-2", AccountID: "acct", State: StateReady}
	if _, err := store.SyncPlanExcessNotices(ctx, []string{"db-1", "db-2"}, map[string]string{"db-2": PlanExcessDatabaseLimit}, at); err != nil {
		t.Fatal(err)
	}
	since, err := store.PlanExcessSince(ctx, []string{"db-1", "db-2"})
	if err != nil || len(since) != 1 || !since["db-2"].Equal(at) {
		t.Fatalf("since = %v, %v; want db-2 at %s", since, err, at)
	}
}

// RetirePastPlan revokes bindings first and schedules the delete only once
// they are gone.
func TestRetirePastPlanRevokesBindingsFirst(t *testing.T) {
	bindings := &retireTestBindings{items: []Binding{{ID: "b-1", State: BindingStateReady}}}
	service := &Service{}
	pending, err := service.RetirePastPlan(context.Background(), bindings, Database{ID: "db-1", AccountID: "acct"})
	if err != nil || !pending || !slices.Equal(bindings.deleted, []string{"b-1"}) {
		t.Fatalf("pending=%v err=%v deleted=%v; want the binding deleted and the delete pending", pending, err, bindings.deleted)
	}
	if _, err := (*Service)(nil).RetirePastPlan(context.Background(), bindings, Database{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil service: %v", err)
	}
}

type retireTestBindings struct {
	items   []Binding
	deleted []string
}

func (b *retireTestBindings) List(context.Context, string, string) ([]Binding, error) {
	return b.items, nil
}

func (b *retireTestBindings) Delete(_ context.Context, _ string, id string) (Binding, error) {
	b.deleted = append(b.deleted, id)
	return Binding{ID: id, State: BindingStateDeleting}, nil
}
