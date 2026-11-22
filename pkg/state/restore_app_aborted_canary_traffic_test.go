package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_RestoreAppReturnsDeletedAppCanaryTraffic(t *testing.T) {
	restoreAppAbortedCanarySuite(t, state.NewMemStore())
}

func TestPg_RestoreAppReturnsDeletedAppCanaryTraffic(t *testing.T) {
	s, _ := pgStore(t)
	restoreAppAbortedCanarySuite(t, s)
}

// Deploy-path probe 2026-11-21 (R2-2): safedeploy closes the canary of a
// deleted app by stamping it aborted, and the deleted app had no traffic to
// hand back. After `gregale app restore` that split kept serving the aborted
// candidate forever (50/50, 10% and 1% splits on prod since 11-15).
func restoreAppAbortedCanarySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	app, stable := abortedCanaryTrafficApp(ctx, t, s)
	candidate := liveTrafficDeployment(ctx, t, s, app, 0)
	if _, err := s.UpdateDeploymentTraffic(ctx, candidate.ID, 10); err != nil {
		t.Fatalf("UpdateDeploymentTraffic(candidate, 10): %v", err)
	}
	abortedAt := time.Now()
	if _, err := s.SafedeployStampRollout(ctx, candidate.ID, "aborted", &abortedAt, nil, &abortedAt, state.RolloutAbortedAppDeleted); err != nil {
		t.Fatalf("SafedeployStampRollout: %v", err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if _, err := s.RestoreApp(ctx, app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	requireTrafficWeights(ctx, t, s, map[string]int{stable.ID: 100, candidate.ID: 0})
}
