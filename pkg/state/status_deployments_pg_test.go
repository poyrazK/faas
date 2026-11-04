package state_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListDeploymentsWithStatusNarrowsListAllDeployments pins imaged's
// narrowed sweep listing to ListAllDeployments filtered by status: the same
// rows in the same order, and none from a deleted app.
func TestPgListDeploymentsWithStatusNarrowsListAllDeployments(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	seed := func(appStatus string, statuses ...string) []state.Deployment {
		t.Helper()
		_, app := seedPgAccountAndApp(t, s, ctx)
		var deps []state.Deployment
		for _, status := range statuses {
			d := seedPgDeployment(t, s, ctx, app)
			if _, err := pool.Exec(ctx, `update deployments set status = $2, traffic_percent = 0 where id = $1`, d.ID, status); err != nil {
				t.Fatalf("update deployment: %v", err)
			}
			deps = append(deps, d)
		}
		// The app status goes last: a deleted app takes no new deployments.
		if _, err := pool.Exec(ctx, `update apps set status = $2 where id = $1`, app.ID, appStatus); err != nil {
			t.Fatalf("update app: %v", err)
		}
		return deps
	}
	seed("active", "live", "superseded", "snapshotting", "failed")
	seed("active", "live")
	deleted := seed("deleted", "live")

	statuses := []state.DeploymentStatus{state.DeployLive, state.DeploySnapshotting}
	got, err := s.ListDeploymentsWithStatus(ctx, statuses...)
	if err != nil {
		t.Fatalf("ListDeploymentsWithStatus: %v", err)
	}
	all, err := s.ListAllDeployments(ctx)
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	var want []string
	for _, d := range all {
		if slices.Contains(statuses, d.Status) {
			want = append(want, d.ID)
		}
	}
	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if len(want) != 3 || !slices.Equal(ids, want) {
		t.Fatalf("ListDeploymentsWithStatus = %v, want %v (deleted app's %s absent)", ids, want, deleted[0].ID)
	}
	if none, err := s.ListDeploymentsWithStatus(ctx); err != nil || len(none) != 0 {
		t.Fatalf("ListDeploymentsWithStatus() = %d rows, %v; want none", len(none), err)
	}
}
