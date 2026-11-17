package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A deleted app's canary has nothing left to progress: meterd read its
// profiling gate every tick and warned "state: not found" for half an hour
// until safedeploy closed the rollout (prod, 2026-11-09). The canary list
// leaves it out; safedeploy's list keeps it so the rollout is still closed.
func TestPgCanaryInFlightSkipsDeletedApp(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	scopedLive(t, ctx, s, app.ID, state.DefaultEnvScope, "stable")
	canary := scopedDeployment(t, ctx, s, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Status: state.DeployBuilding,
		TrafficPercent: 1, TrafficPercentExplicit: true, CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"}, "canary")
	if err := s.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set canary_step = 1, rollout_state = 'rolling_out' where id = $1`, canary.ID); err != nil {
		t.Fatal(err)
	}
	listed := func(rows []state.Deployment) bool {
		for _, d := range rows {
			if d.ID == canary.ID {
				return true
			}
		}
		return false
	}
	if rows, err := s.ListCanaryInFlight(ctx); err != nil || !listed(rows) {
		t.Fatalf("ListCanaryInFlight before delete = %d rows, %v; want the canary", len(rows), err)
	}

	if _, err := pool.Exec(ctx, `update apps set status = 'deleted', deleted_at = now() where id = $1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListCanaryInFlight(ctx); err != nil || listed(rows) {
		t.Fatalf("ListCanaryInFlight after delete = %d rows, %v; want no canary of a deleted app", len(rows), err)
	}
	if rows, err := s.SafedeployListPendingRollouts(ctx); err != nil || !listed(rows) {
		t.Fatalf("SafedeployListPendingRollouts after delete = %d rows, %v; want the rollout to close", len(rows), err)
	}
}
