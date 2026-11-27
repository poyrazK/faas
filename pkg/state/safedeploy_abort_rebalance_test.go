package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-15 → 2026-11-19: apps deleted mid-canary had the canary
// stamped aborted with its traffic kept; after `gregale app restore` they
// still split 90/10, 99/1 and 50/50 onto a dead canary that never parked and
// answered 502. Stamping a live canary aborted now gives its traffic back.
func TestSafedeployStampAbortedGivesCanaryTrafficBack(t *testing.T) {
	ctx := t.Context()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "abort-rebalance@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "abort-rebalance", RAMMB: 256, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, TrafficPercent: 90, RolloutState: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	canary, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, TrafficPercent: 10, RolloutState: "rolling_out", CanaryStep: 1, CanaryTotalSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	// CreateDeployment may normalise weights; pin the 90/10 split.
	m.mu.Lock()
	s, c := m.deployments[stable.ID], m.deployments[canary.ID]
	s.TrafficPercent, c.TrafficPercent, c.RolloutState = 90, 10, "rolling_out"
	m.putDeploymentLocked(s.ID, s)
	m.putDeploymentLocked(c.ID, c)
	m.mu.Unlock()

	now := time.Now()
	if _, err := m.SafedeployStampRollout(ctx, canary.ID, "aborted", c.RolloutStartedAt, nil, &now, "app deleted during rollout"); err != nil {
		t.Fatal(err)
	}
	gotCanary, _ := m.DeploymentByID(ctx, canary.ID)
	gotStable, _ := m.DeploymentByID(ctx, stable.ID)
	if gotCanary.RolloutState != "aborted" || gotCanary.TrafficPercent != 0 {
		t.Fatalf("canary = %s %d%%; want aborted with no traffic", gotCanary.RolloutState, gotCanary.TrafficPercent)
	}
	if gotStable.TrafficPercent != 100 {
		t.Fatalf("stable = %d%%; want the canary's traffic back (100%%)", gotStable.TrafficPercent)
	}
}
