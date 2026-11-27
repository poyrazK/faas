package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type liveDeploymentsStore struct {
	Store state.Store
	live  []state.Deployment
	err   error
}

func (s liveDeploymentsStore) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return s.live, s.err
}

func canaryServer(live []state.Deployment, err error) *server {
	return &server{store: statetest.NewStore(liveDeploymentsStore{Store: state.NewMemStore(), live: live, err: err})}
}

func boaCanaryRows() []state.Deployment {
	now := time.Now()
	return []state.Deployment{
		{ID: "v1", Status: state.DeployLive, TrafficPercent: 99, RolloutState: "complete", CreatedAt: now.Add(-time.Hour)},
		{ID: "v3", Status: state.DeployLive, TrafficPercent: 1, RolloutState: "rolling_out", CanaryStep: 0, CanaryTotalSteps: 3, CreatedAt: now},
	}
}

// boa multi-service deploy (2026-11-21): the traffic refusal during a canary
// named no step, so replacing a broken baseline took an extra status call.
func TestTrafficRefusalDuringCanaryNamesNextCommands(t *testing.T) {
	app := state.App{ID: "a1", Slug: "boa-contacts"}
	problem := canaryServer(boaCanaryRows(), nil).trafficChangeDuringCanaryProblem(context.Background(), app)
	if problem.Code != api.CodeTrafficChangeDuringCanary || problem.Status != http.StatusConflict {
		t.Fatalf("problem = %+v", problem)
	}
	for _, want := range []string{"gregale deployment advance v3 --expected-step 0", "gregale rollouts recover boa-contacts --action promote --expected-step 0"} {
		if !strings.Contains(problem.Hint, want) {
			t.Errorf("hint %q lacks %q", problem.Hint, want)
		}
	}
	generic := canaryServer(nil, errors.New("read failed")).trafficChangeDuringCanaryProblem(context.Background(), app)
	if generic.Code != api.CodeTrafficChangeDuringCanary || generic.Hint != "" {
		t.Fatalf("failed read = %+v, want the generic refusal", generic)
	}
}

func TestRecoverRolloutExpectedStep(t *testing.T) {
	app := state.App{ID: "a1", Slug: "boa-contacts"}
	ctx := context.Background()
	if problem := canaryServer(boaCanaryRows(), nil).recoverRolloutStepProblem(ctx, app, 0); problem != nil {
		t.Fatalf("matching step refused: %+v", problem)
	}
	moved := canaryServer(boaCanaryRows(), nil).recoverRolloutStepProblem(ctx, app, 2)
	if moved == nil || moved.Code != api.CodeCanaryStepConflict {
		t.Fatalf("moved canary = %+v, want canary_step_conflict", moved)
	}
	none := canaryServer(boaCanaryRows()[:1], nil).recoverRolloutStepProblem(ctx, app, 0)
	if none == nil || none.Status != http.StatusNotFound {
		t.Fatalf("no canary = %+v, want 404", none)
	}
}
