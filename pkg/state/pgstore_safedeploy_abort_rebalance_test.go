package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_SafedeployAbortGivesCanaryTrafficBack: an app deleted mid-canary had
// its canary stamped aborted with its weight kept; after `app restore` it kept
// splitting traffic to the dead canary (prod 2026-11-15 → 2026-11-19).
func TestPg_SafedeployAbortGivesCanaryTrafficBack(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, stableID := seedLiveDeploy(t, s, ctx, "abort-rebalance", "abort-rebalance")
	canary := createPgCanary(t, s, ctx, appID, "sha256:abort-rebalance-canary", 4)
	activatePgCanary(t, pool, ctx, canary)
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = case when id = $1 then 10 else 90 end where id in ($1, $2)`, canary, stableID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.SafedeployStampRollout(ctx, canary, "aborted", &now, nil, &now, "app deleted during rollout"); err != nil {
		t.Fatalf("SafedeployStampRollout: %v", err)
	}
	gotCanary, err := s.DeploymentByID(ctx, canary)
	if err != nil {
		t.Fatal(err)
	}
	gotStable, err := s.DeploymentByID(ctx, stableID)
	if err != nil {
		t.Fatal(err)
	}
	if gotCanary.RolloutState != "aborted" || gotCanary.TrafficPercent != 0 {
		t.Fatalf("canary = %s %d%%, want aborted with no traffic", gotCanary.RolloutState, gotCanary.TrafficPercent)
	}
	if gotStable.TrafficPercent != 100 || gotStable.Status != state.DeployLive {
		t.Fatalf("stable = %s %d%%, want live with 100%%", gotStable.Status, gotStable.TrafficPercent)
	}
}
