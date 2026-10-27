package state_test

import (
	"slices"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListFloorDeploymentsKeepsOnlyFloorApps runs the floor candidate SQL
// against real rows: each floor source (app min_instances, policy
// min_instances, policy schedules, a live deployment's min_instances) selects
// all of that app's live deployments, and nothing else is returned.
func TestPgListFloorDeploymentsKeepsOnlyFloorApps(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)

	type seeded struct {
		app  state.App
		deps []state.Deployment
	}
	seed := func(appSQL string, deployments ...string) seeded {
		t.Helper()
		_, app := seedPgAccountAndApp(t, s, ctx)
		out := seeded{app: app}
		for _, depSQL := range deployments {
			d := seedPgDeployment(t, s, ctx, app)
			if _, err := pool.Exec(ctx, `update deployments set `+depSQL+` where id = $1`, d.ID); err != nil {
				t.Fatalf("update deployment: %v", err)
			}
			out.deps = append(out.deps, d)
		}
		// The app columns go last: a deleted app takes no new deployments.
		if _, err := pool.Exec(ctx, `update apps set node_id = $2::uuid`+appSQL+` where id = $1`, app.ID, node); err != nil {
			t.Fatalf("update app: %v", err)
		}
		return out
	}
	const live = `status = 'live'`
	noFloor := seed(``, live)
	appMin := seed(`, min_instances = 1`, live, `status = 'superseded', traffic_percent = 0`)
	policyMin := seed(`, scaling_policy = '{"min_instances": 2}'::jsonb`, live)
	policySchedule := seed(`, scaling_policy = '{"schedules": [{"cron": "0 9 * * *", "duration_s": 60, "min_instances": 1}]}'::jsonb`, live)
	deploymentFloor := seed(``, live+`, min_instances = 2`, live+`, traffic_percent = 0`)
	deleted := seed(`, min_instances = 3, status = 'deleted'`, live)

	got, err := s.ListFloorDeploymentsByNodeID(ctx, node)
	if err != nil {
		t.Fatalf("ListFloorDeploymentsByNodeID: %v", err)
	}
	all, err := s.ListFloorDeployments(ctx)
	if err != nil {
		t.Fatalf("ListFloorDeployments: %v", err)
	}
	want := []string{
		appMin.deps[0].ID,
		policyMin.deps[0].ID,
		policySchedule.deps[0].ID,
		deploymentFloor.deps[0].ID,
		deploymentFloor.deps[1].ID,
	}
	sort.Strings(want)
	for name, deps := range map[string][]state.Deployment{"by node": got, "all": all} {
		ids := make([]string, 0, len(deps))
		for _, d := range deps {
			ids = append(ids, d.ID)
		}
		sort.Strings(ids)
		if !slices.Equal(ids, want) {
			t.Errorf("%s = %v, want %v (no-floor %s, superseded %s, deleted %s must be absent)",
				name, ids, want, noFloor.deps[0].ID, appMin.deps[1].ID, deleted.deps[0].ID)
		}
	}
}
