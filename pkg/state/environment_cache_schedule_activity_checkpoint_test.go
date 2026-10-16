package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCacheScheduleActivityCheckpointMemory(t *testing.T) {
	testActivityCheckpoint(t, state.NewMemStore())
}
func TestCacheScheduleActivityCheckpointPostgres(t *testing.T) {
	testActivityCheckpoint(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testActivityCheckpoint(t *testing.T, s state.Store) {
	f := newActivityFixture(t, s)
	reader := s.(state.EnvironmentCacheScheduleActivityCheckpointReader)
	check := func(floor, head int64) string {
		t.Helper()
		point, err := reader.GetEnvironmentCacheScheduleActivityCheckpoint(t.Context(), f.Account.ID, f.App.ID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		a, e1 := api.ParseEnvironmentCacheActivityCursor(point.OldestCursor, f.Account.ID, f.App.ID, f.Project.ID, f.Env.ID)
		b, e2 := api.ParseEnvironmentCacheActivityCursor(point.RestartCursor, f.Account.ID, f.App.ID, f.Project.ID, f.Env.ID)
		if e1 != nil || e2 != nil || a.Sequence != floor || b.Sequence != head || point.Scope != "staging" || point.AppID != f.App.ID || point.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds {
			t.Fatal(point, e1, e2)
		}
		return point.RestartCursor
	}
	check(0, 0)
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	before := check(0, 2)
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	if page := f.events(t, before, 1); len(page.Entries) != 1 || page.Entries[0].Sequence != 3 {
		t.Fatal("checkpoint lost concurrent creation", page)
	}
	if err := s.(state.EnvironmentCacheScheduleActivityPruner).PruneEnvironmentCacheScheduleActivity(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after := check(3, 3)
	if page := f.events(t, after, 1); len(page.Entries) != 0 {
		t.Fatal(page)
	}
	f.create(t, f.proposal(time.Now().Add(time.Hour)))
	check(3, 4)
	if _, err := reader.GetEnvironmentCacheScheduleActivityCheckpoint(t.Context(), f.Env.ID, f.App.ID, "staging"); err == nil {
		t.Fatal("foreign account read checkpoint")
	}
}
