package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_RecoverRolloutAbortRedistributesTraffic(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "recover-abort")

	canary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:          appID,
		Kind:           state.DeploymentKindImage,
		ImageDigest:    "sha256:recover-abort",
		Status:         state.DeployPending,
		Scope:          "canary",
		TrafficPercent: 25,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(canary): %v", err)
	}

	// CreateDeployment intentionally leaves canary metadata to the deploy
	// handler. Seed the minimal active-rollout state needed to exercise the
	// transactional abort path against the real schema.
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'live', canary_step = 1, canary_total_steps = 4,
		       rollout_state = 'pending'
		 where id = $1`, canary.ID); err != nil {
		t.Fatalf("seed active canary: %v", err)
	}

	aborted, _, err := s.RecoverRollout(ctx, appID, "abort", "manual stop")
	if err != nil {
		t.Fatalf("RecoverRollout(abort): %v", err)
	}
	if aborted.ID != canary.ID || aborted.RolloutState != "aborted" || aborted.TrafficPercent != 0 || aborted.RolloutAbortedReason != "manual stop" {
		t.Fatalf("aborted deployment = %+v", aborted)
	}

	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil {
		t.Fatalf("DeploymentByID(prior): %v", err)
	}
	if prior.TrafficPercent != 100 {
		t.Fatalf("prior traffic = %d, want 100", prior.TrafficPercent)
	}

	live, err := s.LiveDeployments(ctx, appID)
	if err != nil {
		t.Fatalf("LiveDeployments: %v", err)
	}
	if len(live) != 2 || live[0].TrafficPercent+live[1].TrafficPercent != 100 {
		t.Fatalf("live traffic = %+v, want two rows summing to 100", live)
	}
}
