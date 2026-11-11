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
// deleteAt is the date a queued email named, as the mail outbox stores it.
type planExcessSinceTestStore struct {
	*planExcessNoticeTestStore
	deleteAt map[string]time.Time
}

func (s planExcessSinceTestStore) PlanExcessSince(_ context.Context, ids []string) (map[string]PlanExcessClock, error) {
	clocks := map[string]PlanExcessClock{}
	for _, id := range ids {
		if n, ok := s.notices[id]; ok {
			clocks[id] = PlanExcessClock{RecordedAt: n.RecordedAt, DeleteAt: s.deleteAt[id]}
		}
	}
	return clocks, nil
}

// SyncPlanExcessNotices also drops a cleared notice's date, as deleting the
// notice row does.
func (s planExcessSinceTestStore) SyncPlanExcessNotices(ctx context.Context, ids []string, suspended map[string]string, at time.Time) (int, error) {
	for _, id := range ids {
		if _, ok := suspended[id]; !ok {
			delete(s.deleteAt, id)
		}
	}
	return s.planExcessNoticeTestStore.SyncPlanExcessNotices(ctx, ids, suspended, at)
}

func (s planExcessSinceTestStore) RearmPlanExcessNotices(_ context.Context, ids []string, now, staleBefore time.Time) (int, error) {
	rearmed := 0
	for _, id := range ids {
		n, ok := s.notices[id]
		if !ok || !s.deleteAt[id].IsZero() || (!s.mailed[id] && n.RecordedAt.After(staleBefore)) {
			continue
		}
		n.RecordedAt = now
		s.notices[id] = n
		delete(s.mailed, id)
		rearmed++
	}
	return rearmed, nil
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
		// mailFail fails every email; mailOff leaves the email off.
		mailFail, mailOff bool
	}
	var mailed []PlanExcessNotice
	fixture := func() (planExcessSinceTestStore, func(run) (CapSweepSummary, []string)) {
		mailed = nil
		store := planExcessSinceTestStore{&planExcessNoticeTestStore{
			capTestStore: &capTestStore{databases: []Database{database("db-1", "proj-1", 1), database("db-2", "proj-2", 2)}},
			notices:      map[string]PlanExcessNotice{}, mailed: map[string]bool{}}, map[string]time.Time{}}
		return store, func(r run) (CapSweepSummary, []string) {
			t.Helper()
			period := time.Date(r.now.Year(), r.now.Month(), 1, 0, 0, 0, 0, time.UTC)
			for resource, observed := range provider.observed {
				observed.PeriodStart = period
				provider.observed[resource] = observed
			}
			var deleted []string
			notify := func(_ context.Context, n PlanExcessNotice) error {
				if r.mailFail {
					return errors.New("outbox unavailable")
				}
				mailed = append(mailed, n)
				if !store.mailed[n.DatabaseID] {
					store.mailed[n.DatabaseID] = true
					if !n.DeleteAt.IsZero() {
						store.deleteAt[n.DatabaseID] = n.DeleteAt
					}
				}
				return nil
			}
			if r.mailOff {
				notify = nil
			}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementOn, PlanExcessMode: CapEnforcementOn, PlanExcessDeleteMode: r.mode,
				DeletePlanExcess: func(_ context.Context, d Database) (bool, error) {
					deleted = append(deleted, d.ID)
					return r.pending, r.fail
				},
				NotifyPlanExcess: notify,
				Now:              func() time.Time { return r.now }, Qualification: testCapQualification(backend),
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
	t.Run("a database whose email failed is never deleted", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: hobby, mailFail: true})
		if summary, deleted := sweep(run{now: due.Add(time.Hour), mode: CapEnforcementOn, plan: hobby, mailFail: true}); len(deleted) != 0 || summary.PlanExcessDeleteDue != 0 {
			t.Fatalf("deleted without a mailed date: summary %+v, deleted %v", summary, deleted)
		}
	})
	t.Run("a notice mailed while deletion was shadow is mailed again with a date", func(t *testing.T) {
		store, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementShadow, plan: hobby})
		later := due.Add(10 * 24 * time.Hour) // suspended 40 days when deletion turns on
		summary, deleted := sweep(run{now: later, mode: CapEnforcementOn, plan: hobby})
		if len(deleted) != 0 || summary.PlanExcessDeleteRearmed != 1 {
			t.Fatalf("turning deletion on: summary %+v, deleted %v; want the notice restarted, nothing deleted", summary, deleted)
		}
		sweep(run{now: later.Add(time.Minute), mode: CapEnforcementOn, plan: hobby})
		redue := later.Add(api.ManagedPostgresPlanExcessDeleteAfter)
		if len(mailed) != 2 || !mailed[1].DeleteAt.Equal(redue) || !store.deleteAt["db-2"].Equal(redue) {
			t.Fatalf("mailed %+v, stored %v; want a second email naming %s", mailed, store.deleteAt, redue)
		}
		if _, deleted := sweep(run{now: redue.Add(-time.Second), mode: CapEnforcementOn, plan: hobby}); len(deleted) != 0 {
			t.Fatalf("deleted before the mailed date: %v", deleted)
		}
		if _, deleted := sweep(run{now: redue, mode: CapEnforcementOn, plan: hobby}); !slices.Equal(deleted, []string{"db-2"}) {
			t.Fatalf("deleted %v at the mailed date; want db-2", deleted)
		}
	})
	t.Run("a notice never mailed in its window is restarted", func(t *testing.T) {
		_, sweep := fixture()
		sweep(run{now: start, mode: CapEnforcementOn, plan: hobby, mailOff: true})
		if summary, deleted := sweep(run{now: due, mode: CapEnforcementOn, plan: hobby, mailOff: true}); len(deleted) != 0 || summary.PlanExcessDeleteRearmed != 1 {
			t.Fatalf("summary %+v, deleted %v; want the unmailed notice restarted and nothing deleted", summary, deleted)
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
	for _, id := range []string{"db-1", "db-2", "db-3"} {
		store.databases[id] = Database{ID: id, AccountID: "acct", State: StateReady}
	}
	ids := []string{"db-1", "db-2", "db-3"}
	if _, err := store.SyncPlanExcessNotices(ctx, ids, map[string]string{"db-2": PlanExcessDatabaseLimit, "db-3": PlanExcessDatabaseLimit}, at); err != nil {
		t.Fatal(err)
	}
	clocks, err := store.PlanExcessSince(ctx, ids)
	if err != nil || len(clocks) != 2 || !clocks["db-2"].RecordedAt.Equal(at) || !clocks["db-2"].DeleteAt.IsZero() {
		t.Fatalf("clocks = %v, %v; want db-2 and db-3 at %s with no date", clocks, err, at)
	}
	deleteAt := at.Add(api.ManagedPostgresPlanExcessDeleteAfter)
	store.markPlanExcessNoticeMailed("db-2", deleteAt)
	// db-3 is still inside its mailing window: not restarted.
	now := at.Add(time.Hour)
	if n, err := store.RearmPlanExcessNotices(ctx, ids, now, now.Add(-api.InternalMailManagedPostgresPlanExcessTTL)); err != nil || n != 0 {
		t.Fatalf("rearm inside the window = %d, %v", n, err)
	}
	// A day later db-3 was never mailed; db-2's email named a date and stays.
	now = at.Add(25 * time.Hour)
	if n, err := store.RearmPlanExcessNotices(ctx, ids, now, now.Add(-api.InternalMailManagedPostgresPlanExcessTTL)); err != nil || n != 1 {
		t.Fatalf("rearm past the window = %d, %v; want db-3 only", n, err)
	}
	clocks, _ = store.PlanExcessSince(ctx, ids)
	if !clocks["db-2"].DeleteAt.Equal(deleteAt) || !clocks["db-2"].RecordedAt.Equal(at) || !clocks["db-3"].RecordedAt.Equal(now) {
		t.Fatalf("clocks after rearm = %+v", clocks)
	}
	if !clocks["db-2"].Due(deleteAt) || clocks["db-2"].Due(deleteAt.Add(-time.Second)) || clocks["db-3"].Due(now.AddDate(1, 0, 0)) {
		t.Fatal("Due: want only db-2, only from its mailed date")
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
