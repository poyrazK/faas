package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPgListLiveInstancesForAppsSkipsHistory runs the reaper's bulk read
// against real rows: only the asked apps' live instances come back, grouped
// by app with the newest start first, and parked or stopped history does not.
func TestPgListLiveInstancesForAppsSkipsHistory(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	insert := func(appID, deploymentID, st string, startedMinutes int) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			insert into instances (id, app_id, deployment_id, node_id, state, ram_mb, started_at)
			values ($1, $2, $3, $4, $5, 256, $6)`,
			id, appID, deploymentID, node, st, base.Add(time.Duration(startedMinutes)*time.Minute)); err != nil {
			t.Fatalf("insert %s instance: %v", st, err)
		}
		return id
	}
	_, a := seedPgAccountAndApp(t, s, ctx)
	da := seedPgDeployment(t, s, ctx, a)
	_, b := seedPgAccountAndApp(t, s, ctx)
	db := seedPgDeployment(t, s, ctx, b)
	_, other := seedPgAccountAndApp(t, s, ctx)
	dother := seedPgDeployment(t, s, ctx, other)

	aOld := insert(a.ID, da.ID, "running", 1)
	aNew := insert(a.ID, da.ID, "frozen", 3)
	insert(a.ID, da.ID, "parked", 4)
	insert(a.ID, da.ID, "stopped", 5)
	bDraining := insert(b.ID, db.ID, "draining", 2)
	insert(b.ID, db.ID, "parked", 6)
	insert(other.ID, dother.ID, "running", 7)

	got, err := s.ListLiveInstancesForApps(ctx, []string{a.ID, b.ID})
	if err != nil {
		t.Fatalf("ListLiveInstancesForApps: %v", err)
	}
	byApp := map[string][]string{}
	for _, ins := range got {
		byApp[ins.AppID] = append(byApp[ins.AppID], ins.ID)
	}
	if want := []string{aNew, aOld}; !slices.Equal(byApp[a.ID], want) {
		t.Errorf("app a = %v, want %v (live only, newest first)", byApp[a.ID], want)
	}
	if want := []string{bDraining}; !slices.Equal(byApp[b.ID], want) {
		t.Errorf("app b = %v, want %v", byApp[b.ID], want)
	}
	if len(got) != 3 {
		t.Errorf("returned %d rows, want 3 (no history, no other app)", len(got))
	}
	if none, err := s.ListLiveInstancesForApps(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("no apps = %v (err %v), want none", none, err)
	}
}
