package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 20: `rollouts recover --action advance` moves a stuck canary to the
// next stage of its own ladder (H20-1), and the safedeploy orchestrator's
// stamp never overwrites a rollout that changed after it was listed (H20-2).

// placeCanary puts a rolling_out canary on a stage, stuck since long enough
// for recovery.
type placeCanary func(t *testing.T, id string, step int)

func TestRolloutRecoveryLadderMem(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "rollout-ladder-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) string {
		t.Helper()
		app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	place := func(t *testing.T, id string, step int) {
		t.Helper()
		d, err := m.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		stuck := time.Now().Add(-state.RecoverRolloutStuckAfter - time.Minute)
		if err := m.SetDeploymentCanaryState(ctx, id, d.CanaryPreset, step, d.CanaryTotalSteps, stuck, "rolling_out"); err != nil {
			t.Fatal(err)
		}
	}
	runRolloutRecoveryLadder(t, ctx, m, newApp, place)
}

func TestRolloutRecoveryLadderPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, _ := seedPgAccountAndApp(t, s, ctx)
	newApp := func(slug string) string {
		t.Helper()
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	place := func(t *testing.T, id string, step int) {
		t.Helper()
		stuck := time.Now().Add(-state.RecoverRolloutStuckAfter - time.Minute)
		if _, err := pool.Exec(ctx, `update deployments set canary_step = $2, canary_step_started_at = $3, rollout_state = 'rolling_out' where id = $1`, id, step, stuck); err != nil {
			t.Fatal(err)
		}
	}
	runRolloutRecoveryLadder(t, ctx, s, newApp, place)
}

func runRolloutRecoveryLadder(t *testing.T, ctx context.Context, s state.Store, newApp func(string) string, place placeCanary) {
	custom, err := json.Marshal([]canarycatalog.CustomStage{
		{Percent: 1, Duration: "1m"}, {Percent: 2, Duration: "1m"}, {Percent: 5, Duration: "1m"}, {Percent: 20, Duration: "1m"}, {Percent: 100, Duration: "0s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		preset       string
		stages       json.RawMessage
		total        int
		from         int
		wantStep     int
		wantPercent  int
		wantComplete bool
	}{
		// balanced is 1/10/50/100: the old table kept 10% here.
		{name: "balanced 10% advances to 50%", preset: "balanced", total: 4, from: 1, wantStep: 2, wantPercent: 50},
		// The old table kept 50% at step 3, where nothing completed it.
		{name: "balanced 50% completes", preset: "balanced", total: 4, from: 2, wantStep: 4, wantPercent: 100, wantComplete: true},
		// slow is 1/10/100; the old table used aggressive's 5/50.
		{name: "slow 1% advances to 10%", preset: "slow", total: 3, from: 0, wantStep: 1, wantPercent: 10},
		{name: "slow 10% completes", preset: "slow", total: 3, from: 1, wantStep: 3, wantPercent: 100, wantComplete: true},
		// A five-stage custom ladder had no table row and went to 0%.
		{name: "custom ladder follows its stages", preset: "custom", stages: custom, total: 5, from: 1, wantStep: 2, wantPercent: 5},
		// A row the old table stranded on its last stage completes.
		{name: "stranded last stage completes", preset: "balanced", total: 4, from: 3, wantStep: 4, wantPercent: 100, wantComplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appID := newApp("ladder-" + uuid.NewString()[:8])
			stable := scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "stable")
			canary := scopedDeployment(t, ctx, s, state.Deployment{AppID: appID, Scope: state.DefaultEnvScope, Status: state.DeployBuilding,
				TrafficPercent: 1, TrafficPercentExplicit: true, CanaryPreset: tc.preset, CanaryStages: tc.stages, CanaryTotalSteps: tc.total, RolloutState: "pending"}, "canary")
			if err := s.MarkDeploymentLive(ctx, canary.ID); err != nil {
				t.Fatal(err)
			}
			place(t, canary.ID, tc.from)

			got, _, err := s.RecoverRollout(ctx, appID, "advance", "stuck")
			if err != nil {
				t.Fatalf("RecoverRollout advance: %v", err)
			}
			wantState := "rolling_out"
			if tc.wantComplete {
				wantState = "complete"
			}
			if got.CanaryStep != tc.wantStep || got.TrafficPercent != tc.wantPercent || got.RolloutState != wantState {
				t.Fatalf("advanced canary = step %d at %d%% (%s), want step %d at %d%% (%s)",
					got.CanaryStep, got.TrafficPercent, got.RolloutState, tc.wantStep, tc.wantPercent, wantState)
			}
			if prev, err := s.DeploymentByID(ctx, stable.ID); err != nil || prev.TrafficPercent != 100-tc.wantPercent {
				t.Fatalf("stable = %d%% (err %v), want %d%%", prev.TrafficPercent, err, 100-tc.wantPercent)
			}
		})
	}

	t.Run("a stale stamp keeps an abort", func(t *testing.T) {
		appID := newApp("stamp-" + uuid.NewString()[:8])
		scopedLive(t, ctx, s, appID, state.DefaultEnvScope, "stable")
		canary := scopedCanary(t, ctx, s, appID, "canary")
		// The orchestrator listed the rollout; APID aborts it before the stamp.
		if _, _, err := s.RecoverRollout(ctx, appID, "abort", "operator abort"); err != nil {
			t.Fatalf("RecoverRollout abort: %v", err)
		}
		for _, next := range []string{"complete", "rolling_out"} {
			now := time.Now()
			if _, err := s.SafedeployStampRollout(ctx, canary.ID, next, &now, &now, nil, ""); !errors.Is(err, state.ErrRolloutStateInvalid) {
				t.Fatalf("stamp %s over an abort = %v, want ErrRolloutStateInvalid", next, err)
			}
		}
		d, err := s.DeploymentByID(ctx, canary.ID)
		if err != nil || d.RolloutState != "aborted" || d.RolloutAbortedAt == nil || d.RolloutAbortedReason != "operator abort" || d.TrafficPercent != 0 {
			t.Fatalf("aborted canary = %+v (err %v), want it still aborted at 0%% with its reason", d, err)
		}
	})
}
