package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedServiceRollout(t *testing.T, s *state.PgStore) (state.Deployment, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "service-rollout-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID:      account.ID,
		Slug:           "service-rollout-" + uuid.NewString()[:8],
		Type:           state.AppTypeApp,
		RAMMB:          128,
		MaxConcurrency: 5,
		IdleTimeoutS:   60,
		Manifest: state.AppManifest{
			ExecutionMode: api.ExecutionModeService,
			ServiceReplicas: &state.ServiceReplicas{
				Min: 1, Max: 3, Desired: 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	stable, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:          app.ID,
		Kind:           state.DeploymentKindImage,
		ImageDigest:    "sha256:service-stable",
		Status:         state.DeployPending,
		Scope:          "default",
		TrafficPercent: 100,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(stable): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(stable): %v", err)
	}

	started := time.Now().UTC()
	rollout, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:            app.ID,
		Kind:             state.DeploymentKindImage,
		ImageDigest:      "sha256:service-next",
		Status:           state.DeployPending,
		Scope:            "default",
		TrafficPercent:   0,
		RolloutState:     "rolling_out",
		RolloutStartedAt: &started,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(rollout): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, rollout.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(rollout): %v", err)
	}
	return stable, rollout
}

func TestPgStoreServiceRolloutIndexAllowsReadinessOverlap(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	stable, rollout := seedServiceRollout(t, s)

	var indexDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		 where schemaname = current_schema() and indexname = 'deployments_app_scope_live_uniq'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("read service rollout index: %v", err)
	}
	if !strings.Contains(indexDef, "rollout_state <> 'rolling_out'::text") {
		t.Fatalf("service rollout index = %q, want rolling_out exclusion", indexDef)
	}

	if got, err := s.LiveDeploymentForScope(ctx, stable.AppID, "default"); err != nil || got.ID != stable.ID {
		t.Fatalf("LiveDeploymentForScope before promotion = %q, %v; want stable %q", got.ID, err, stable.ID)
	}
	if got, err := s.DeploymentByID(ctx, rollout.ID); err != nil || got.RolloutState != "rolling_out" || got.TrafficPercent != 0 {
		t.Fatalf("rollout before promotion = state:%q traffic:%d err:%v; want rolling_out/0", got.RolloutState, got.TrafficPercent, err)
	}
}

func TestPgStoreFinalizeServiceRolloutSupersedesPrevious(t *testing.T) {
	s, _, _ := pgWithPool(t)
	stable, rollout := seedServiceRollout(t, s)

	updated, err := s.FinalizeServiceRollout(t.Context(), rollout.ID)
	if err != nil {
		t.Fatalf("FinalizeServiceRollout: %v", err)
	}
	if updated.ID != rollout.ID || updated.Status != state.DeployLive || updated.TrafficPercent != 100 || updated.RolloutState != "complete" {
		t.Fatalf("finalized rollout = %+v; want live/100/complete", updated)
	}
	old, err := s.DeploymentByID(t.Context(), stable.ID)
	if err != nil {
		t.Fatalf("read superseded stable: %v", err)
	}
	if old.Status != state.DeploySuperseded || old.TrafficPercent != 0 {
		t.Fatalf("stable after finalize = status:%q traffic:%d; want superseded/0", old.Status, old.TrafficPercent)
	}

	if _, err := s.FinalizeServiceRollout(t.Context(), rollout.ID); !errors.Is(err, state.ErrServiceRolloutInvalid) {
		t.Fatalf("second FinalizeServiceRollout error = %v, want ErrServiceRolloutInvalid", err)
	}
}

func TestPgStoreAbortServiceRolloutRestoresPrevious(t *testing.T) {
	s, _, _ := pgWithPool(t)
	stable, rollout := seedServiceRollout(t, s)

	updated, err := s.AbortServiceRollout(t.Context(), rollout.ID, "readiness timeout")
	if err != nil {
		t.Fatalf("AbortServiceRollout: %v", err)
	}
	if updated.ID != rollout.ID || updated.Status != state.DeploySuperseded || updated.TrafficPercent != 0 || updated.RolloutState != "aborted" || updated.RolloutAbortedReason != "readiness timeout" {
		t.Fatalf("aborted rollout = %+v; want superseded/0/aborted/reason", updated)
	}
	old, err := s.DeploymentByID(t.Context(), stable.ID)
	if err != nil {
		t.Fatalf("read restored stable: %v", err)
	}
	if old.Status != state.DeployLive || old.TrafficPercent != 100 {
		t.Fatalf("stable after abort = status:%q traffic:%d; want live/100", old.Status, old.TrafficPercent)
	}

	if _, err := s.AbortServiceRollout(t.Context(), rollout.ID, "again"); !errors.Is(err, state.ErrServiceRolloutInvalid) {
		t.Fatalf("second AbortServiceRollout error = %v, want ErrServiceRolloutInvalid", err)
	}
}
