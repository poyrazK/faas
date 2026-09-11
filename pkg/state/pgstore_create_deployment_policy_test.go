//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCreateDeployment_ParkedAppPreservesCustomCanary(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "parked-custom-canary@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "parked-custom-canary",
		Type:      state.AppTypeApp,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatal(err)
	}

	prior, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:prior",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	prior, err = s.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.RolloutState != "complete" || prior.RolloutCompletedAt == nil {
		t.Fatalf("stable deployment rollout = state:%q completed_at:%v; want complete with terminal timestamp", prior.RolloutState, prior.RolloutCompletedAt)
	}

	parked := state.AppEvictedCold
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	stages := json.RawMessage(`[{"percent":10,"duration":"15s"},{"percent":100,"duration":"0s"}]`)
	candidate, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:                  app.ID,
		Kind:                   state.DeploymentKindTarball,
		SourcePath:             "/tmp/custom-canary.tar.gz",
		TrafficPercent:         10,
		CanaryPreset:           "custom",
		CanaryStep:             0,
		CanaryTotalSteps:       2,
		CanaryStepStartedAt:    &startedAt,
		CanaryStages:           stages,
		RolloutState:           "pending",
		TrafficPercentExplicit: true,
	})
	if err != nil {
		t.Fatalf("CreateDeployment on parked app: %v", err)
	}
	if candidate.CanaryPreset != "custom" || candidate.CanaryStep != 0 || candidate.CanaryTotalSteps != 2 || candidate.TrafficPercent != 10 {
		t.Fatalf("created deployment lost canary policy: %+v", candidate)
	}
	if !jsonEqual(candidate.CanaryStages, stages) {
		t.Fatalf("created stages = %s, want %s", candidate.CanaryStages, stages)
	}
	if candidate.CanaryStepStartedAt == nil || !candidate.CanaryStepStartedAt.Equal(startedAt) {
		t.Fatalf("created canary start = %v, want %v", candidate.CanaryStepStartedAt, startedAt)
	}

	if err := s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatalf("MarkDeploymentLive candidate: %v", err)
	}
	candidate, err = s.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior, err = s.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Status != state.DeployLive || candidate.TrafficPercent != 10 || candidate.RolloutState != "rolling_out" {
		t.Fatalf("candidate initial rollout = %+v, want live/10/rolling_out", candidate)
	}
	if prior.Status != state.DeployLive || prior.TrafficPercent != 90 {
		t.Fatalf("prior initial rollout = %+v, want live/90", prior)
	}

	accountID := uuid.MustParse(account.ID)
	candidate, _, err = s.AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{
		ExpectedStep:   0,
		TrafficPercent: 100,
		Audit: state.DeploymentAudit{
			AccountID: &accountID,
			Kind:      state.DeployTrafficChanged,
			Actor:     "test",
		},
	})
	if err != nil {
		t.Fatalf("AdvanceCanary: %v", err)
	}
	prior, err = s.DeploymentByID(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.TrafficPercent != 100 || candidate.CanaryStep != candidate.CanaryTotalSteps || candidate.RolloutState != "complete" {
		t.Fatalf("completed candidate = %+v, want 100%% terminal", candidate)
	}
	if prior.Status != state.DeploySuperseded || prior.TrafficPercent != 0 {
		t.Fatalf("completed prior = %+v, want superseded/0", prior)
	}
	gotApp, err := s.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want parked throughout deployment", gotApp.Status)
	}

	if err := s.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("CreateDeployment on deleted app error = %v, want ErrNotFound", err)
	}
}
