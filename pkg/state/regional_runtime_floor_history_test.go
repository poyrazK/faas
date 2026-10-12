package state

// adr: 801
import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type regionalFloorFixture struct {
	store Store
	app   App
	dep   Deployment
}

func newRegionalFloorFixture(t *testing.T, s Store, account Account) regionalFloorFixture {
	t.Helper()
	app, err := s.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: account.ID, Slug: "floor-history-" + uuid.NewString()[:8], Type: AppTypeApp, Status: AppActive, RAMMB: 128, MaxConcurrency: 3, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc", Status: DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	return regionalFloorFixture{s, app, dep}
}

// Merge minute pages only for testing retention across a wall-clock boundary;
// production pages themselves never claim a complete snapshot.
func floorHistoryRows(t *testing.T, s RegionalRuntimeFloorHistoryStore, app string, from time.Time) []RegionalRuntimeFloorInterval {
	t.Helper()
	byID := map[int64]RegionalRuntimeFloorInterval{}
	through := time.Now().UTC().Add(time.Minute).Truncate(time.Minute)
	for minute := from.UTC().Truncate(time.Minute); !minute.After(through); minute = minute.Add(time.Minute) {
		var after int64
		for {
			page, err := s.ListRegionalRuntimeFloorIntervals(t.Context(), regionalQuotaCanonicalID(app), minute, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, r := range page {
				byID[r.ID] = r
			}
			if page[len(page)-1].ID <= after {
				t.Fatal("floor page did not advance")
			}
			after = page[len(page)-1].ID
		}
	}
	rows := make([]RegionalRuntimeFloorInterval, 0, len(byID))
	for _, r := range byID {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func runRegionalFloorHistoryContract(t *testing.T, open func(*testing.T) regionalFloorFixture) {
	t.Helper()
	t.Run("original-policy-and-resource-history", func(t *testing.T) {
		f := open(t)
		ctx := t.Context()
		rows := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if len(rows) != 2 || rows[0].App == nil || rows[0].App.RAMMB != 128 || rows[0].App.MinInstances != 0 || rows[1].Deployment == nil || rows[1].ObjectID != regionalQuotaCanonicalID(f.dep.ID) {
			t.Fatal(rows)
		}
		if err := f.store.SetAppMinInstances(ctx, f.app.ID, 1); err != nil {
			t.Fatal(err)
		}
		policy := &ScalingPolicy{MinInstances: 2, Timezone: "Europe/Istanbul", Schedules: []ScalingSchedule{{Cron: "* * * * *", DurationS: 30, MinInstances: 3}}}
		ram := 256
		if _, err := f.store.UpdateApp(ctx, f.app.ID, UpdateAppParams{RAMMB: &ram, ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.UpdateDeploymentMinInstances(ctx, f.dep.ID, 4); err != nil {
			t.Fatal(err)
		}
		rows = floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if len(rows) != 5 || rows[0].EndedAt == nil || rows[0].App.RAMMB != 128 || rows[0].App.MinInstances != 0 || rows[2].App.MinInstances != 1 || rows[3].App.RAMMB != 256 || rows[3].App.ScalingPolicy.MinInstances != 2 || rows[3].App.ScalingPolicy.Timezone != "Europe/Istanbul" || len(rows[3].App.ScalingPolicy.Schedules) != 1 || rows[4].Deployment.MinInstances != 4 {
			t.Fatal(rows)
		}
		historical := App{MinInstances: rows[3].App.MinInstances, ScalingPolicy: rows[3].App.ScalingPolicy}
		minute := rows[3].StartedAt.UTC().Truncate(time.Minute)
		if got := historical.EffectiveMinInstanceSecondsInMinute(minute, 0); got != 150 {
			t.Fatalf("original scheduled floor = %d, want 150", got)
		}
		unchanged := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if err := f.store.SetAppMinInstances(ctx, f.app.ID, rows[3].App.MinInstances); err != nil {
			t.Fatal(err)
		}
		policy.Target = &ScalingTarget{Metric: "rps", Value: 10}
		if _, err := f.store.UpdateApp(ctx, f.app.ID, UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
			t.Fatal(err)
		}
		if got := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, unchanged) {
			t.Fatal("unchanged floor reset history", got)
		}
		rows[3].App.ScalingPolicy.Schedules[0].MinInstances = 99
		rows[3].App.RAMMB = 999
		*rows[0].EndedAt = time.Time{}
		if got := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, unchanged) {
			t.Fatal("returned floor history aliases retention", got)
		}
		ram = 512
		if _, err := f.store.UpdateApp(ctx, f.app.ID, UpdateAppParams{RAMMB: &ram}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.UpdateDeploymentMinInstances(ctx, f.dep.ID, 0); err != nil {
			t.Fatal(err)
		}
		preserved := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if len(preserved) != 7 || !reflect.DeepEqual(preserved[0].App, unchanged[0].App) || !reflect.DeepEqual(preserved[3].App, unchanged[3].App) {
			t.Fatal("current settings replaced old promise", preserved)
		}
	})
	t.Run("collection-retains-floor-intent", func(t *testing.T) {
		f := open(t)
		ctx := t.Context()
		if err := f.store.SetAppMinInstances(ctx, f.app.ID, 2); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.UpdateDeploymentMinInstances(ctx, f.dep.ID, 3); err != nil {
			t.Fatal(err)
		}
		prior := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if _, err := f.store.ScheduleAppDeletion(ctx, f.app.ID, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		deleted := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if len(deleted) != len(prior)+1 || deleted[len(deleted)-1].App.Status != AppDeleted {
			t.Fatal(deleted)
		}
		// Exercise the actual guarded purge path after the grace deadline.
		grace := time.Now().Add(-time.Hour)
		if _, err := f.store.ScheduleAppDeletion(ctx, f.app.ID, grace); err != nil {
			t.Fatal(err)
		}
		if err := f.store.ClaimAppDeletion(ctx, f.app.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteAppPermanently(ctx, f.app.ID); err != nil {
			t.Fatal(err)
		}
		after := floorHistoryRows(t, f.store, f.app.ID, f.app.CreatedAt)
		if len(after) != len(deleted) {
			t.Fatal(after)
		}
		for i, r := range after {
			if r.EndedAt == nil || !reflect.DeepEqual(r.App, deleted[i].App) || !reflect.DeepEqual(r.Deployment, deleted[i].Deployment) {
				t.Fatal("purge erased original floor", after)
			}
		}
		minute := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
		if page, err := f.store.ListRegionalRuntimeFloorIntervals(ctx, regionalQuotaCanonicalID(f.app.ID), minute, 0, 20); err != nil || len(page) != 0 {
			t.Fatal("purged intent continued", page, err)
		}
	})
	t.Run("pagination-install-and-context", func(t *testing.T) {
		f := open(t)
		start, err := f.store.RegionalRuntimeFloorHistoryStart(t.Context())
		if err != nil || start.IsZero() {
			t.Fatal(start, err)
		}
		minute := time.Now().UTC().Truncate(time.Minute)
		for _, test := range []struct {
			app    string
			minute time.Time
			after  int64
			limit  int
		}{
			{"bad", minute, 0, 1}, {regionalQuotaCanonicalID(f.app.ID), minute.Add(time.Microsecond), 0, 1}, {regionalQuotaCanonicalID(f.app.ID), minute, -1, 1}, {regionalQuotaCanonicalID(f.app.ID), minute, 0, 0},
		} {
			if _, err := f.store.ListRegionalRuntimeFloorIntervals(t.Context(), test.app, test.minute, test.after, test.limit); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.store.ListRegionalRuntimeFloorIntervals(ctx, regionalQuotaCanonicalID(f.app.ID), minute, 0, 1); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := f.store.RegionalRuntimeFloorHistoryStart(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestMemRegionalRuntimeFloorHistoryContract(t *testing.T) {
	runRegionalFloorHistoryContract(t, func(t *testing.T) regionalFloorFixture {
		s := NewMemStore()
		a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@floor-history.example", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newRegionalFloorFixture(t, s, a)
	})
}

func TestMemRegionalRuntimeFloorHistoryProposalAndRollback(t *testing.T) {
	s := NewMemStore()
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@floor-proposal.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := newRegionalFloorFixture(t, s, a)
	before := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt)
	s.mu.Lock()
	old := s.apps[f.app.ID]
	next := old
	next.RAMMB = 512
	rollback := s.regionalFloorRollbackLocked()
	s.putAppLocked(f.app.ID, next)
	s.putAppLocked(f.app.ID, old)
	rollback()
	s.mu.Unlock()
	if got := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, before) {
		t.Fatal("rollback retained proposed floor", got)
	}
	s.mu.Lock()
	s.serviceCapacityProtection = true
	_ = s.checkServiceCapacityAppLocked(next)
	_ = s.checkServiceCapacityDeploymentLocked(f.dep)
	s.serviceCapacityProtection = false
	s.mu.Unlock()
	if got := floorHistoryRows(t, s, f.app.ID, f.app.CreatedAt); !reflect.DeepEqual(got, before) {
		t.Fatal("proposal check retained floor history", got)
	}
	// Exercise a real compound API failure after an app was inserted.
	entry := OrgActivity{OrgID: uuid.New(), Kind: "invalid"}
	if _, _, err := s.CreateAppIfUnderQuotaWithActivity(t.Context(), App{ID: uuid.NewString(), AccountID: a.ID, Slug: "failed-floor", RAMMB: 128, Status: AppActive}, api.MustLimitsFor(api.PlanPro), entry); err == nil {
		t.Fatal("expected activity rejection")
	}
	s.mu.Lock()
	count := len(s.regionalRuntimeFloorRows)
	s.mu.Unlock()
	if count != len(before) {
		t.Fatalf("failed app creation leaked floor history: %d", count)
	}
}
