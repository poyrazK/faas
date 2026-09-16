package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_AutoRollbackDeploymentsTxNormalizesReleaseProjection(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	target := seedPgDeployment(t, s, ctx, app)
	current := seedPgDeployment(t, s, ctx, app)
	sibling := seedPgDeployment(t, s, ctx, app)

	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'superseded', traffic_percent = 100,
		       rollout_state = 'aborted', rollout_aborted_at = $2,
		       rollout_aborted_reason = 'old failure'
		 where id = $1`, target.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'live', traffic_percent = 0,
		       rollout_state = 'rolling_out', rollout_started_at = $2,
		       rollout_completed_at = null
		 where id = $1`, current.ID, now); err != nil {
		t.Fatalf("seed current: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'live', traffic_percent = 100,
		       rollout_state = 'complete', rollout_completed_at = $2
		 where id = $1`, sibling.ID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed sibling: %v", err)
	}

	gotID, err := s.AutoRollbackDeploymentsTx(ctx, app.ID, current.ID)
	if err != nil {
		t.Fatalf("AutoRollbackDeploymentsTx: %v", err)
	}
	if gotID != target.ID {
		t.Fatalf("target id = %s, want %s", gotID, target.ID)
	}

	gotTarget, err := s.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(target): %v", err)
	}
	if gotTarget.Status != state.DeployLive || gotTarget.TrafficPercent != 100 || gotTarget.RolloutState != "complete" || gotTarget.RolloutCompletedAt == nil || gotTarget.RolloutAbortedAt != nil || gotTarget.RolloutAbortedReason != "" {
		t.Fatalf("target projection = %+v", gotTarget)
	}
	for _, id := range []string{current.ID, sibling.ID} {
		got, err := s.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatalf("DeploymentByID(%s): %v", id, err)
		}
		if got.Status != state.DeploySuperseded || got.TrafficPercent != 0 || got.RolloutState != "aborted" || got.RolloutAbortedAt == nil || got.RolloutCompletedAt != nil {
			t.Fatalf("retired projection %s = %+v", id, got)
		}
	}
	gotCurrent, err := s.DeploymentByID(ctx, current.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(current): %v", err)
	}
	if gotCurrent.LastAutoRollbackAt == nil || gotCurrent.LastAutoRollbackReason != "threshold_exceeded" {
		t.Fatalf("auto rollback audit anchor = %+v", gotCurrent)
	}
}

func TestPg_PrepareDeploymentRollbackPreservesCurrentLive(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	target := seedPgDeployment(t, s, ctx, app)
	current := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'superseded', traffic_percent = 100,
		       traffic_percent_explicit = true,
		       canary_preset = 'balanced', canary_step = 2, canary_total_steps = 4,
		       rollout_state = 'aborted', rollout_aborted_at = $2,
		       rollout_aborted_reason = 'old failure'
		 where id = $1`, target.ID, now); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update deployments
		   set status = 'live', traffic_percent = 100,
		       rollout_state = 'complete', rollout_completed_at = $2
		 where id = $1`, current.ID, now); err != nil {
		t.Fatalf("seed current: %v", err)
	}

	prepared, err := s.PrepareDeploymentRollback(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if prepared.Status != state.DeploySnapshotting || prepared.TrafficPercent != 0 || prepared.TrafficPercentExplicit || prepared.CanaryPreset != "none" || prepared.CanaryStep != 0 || prepared.CanaryTotalSteps != 0 || prepared.RolloutState != "pending" || prepared.RolloutStartedAt != nil || prepared.RolloutCompletedAt != nil || prepared.RolloutAbortedAt != nil {
		t.Fatalf("prepared target = %+v", prepared)
	}
	gotCurrent, err := s.DeploymentByID(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotCurrent.Status != state.DeployLive || gotCurrent.TrafficPercent != 100 {
		t.Fatalf("current deployment changed = %+v", gotCurrent)
	}
}
