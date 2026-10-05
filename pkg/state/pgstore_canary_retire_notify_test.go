// adr: 199 — a completed canary hands its retired stable deployment to schedd for draining.
package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_AdvanceCanaryTerminalNotifiesRetiredSibling reproduces the
// production-us rollback stall. Completing a canary superseded the old stable
// deployment in SQL but published only a traffic change for the candidate. So
// schedd never drained the stable deployment's instance. It kept a
// max_concurrency=1 app's only rollout slot for 10 minutes and blocked the
// next rollback. The retirement must publish status=superseded for the sibling
// in the same commit.
func TestPg_AdvanceCanaryTerminalNotifiesRetiredSibling(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "canary-retire-notify", "canary-retire-notify")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:canary-retire-notify",
		Status: state.DeployPending, Scope: "default", CanaryTotalSteps: 1, TrafficPercent: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status = 'live', rollout_state = 'rolling_out', canary_step = 0, traffic_percent = 20 where id = $1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set traffic_percent = 80 where id = $1`, priorID); err != nil {
		t.Fatal(err)
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `listen deployment_changed`); err != nil {
		t.Fatal(err)
	}

	advanced, _, err := s.AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{
		ExpectedStep: 0, TrafficPercent: 100,
		Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test"},
	})
	if err != nil || advanced.TrafficPercent != 100 {
		t.Fatalf("terminal advance = %+v, err=%v", advanced, err)
	}
	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil || prior.Status != state.DeploySuperseded {
		t.Fatalf("former stable = %+v, err=%v; want superseded", prior, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		notification, err := listener.Conn().WaitForNotification(waitCtx)
		if err != nil {
			t.Fatalf("no deployment_changed status=superseded for the retired sibling %s: %v", priorID, err)
		}
		var payload struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
			Status       string `json:"status"`
		}
		if json.Unmarshal([]byte(notification.Payload), &payload) != nil {
			continue
		}
		if payload.DeploymentID == priorID && payload.Status == string(state.DeploySuperseded) && payload.AppID == appID {
			return
		}
	}
}
