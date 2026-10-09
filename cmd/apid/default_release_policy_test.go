package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/state"
)

type releasePolicyFakeStore struct {
	live      bool
	lookupErr error
	leaseOK   bool
	leaseErr  error
}

func (f releasePolicyFakeStore) LiveDeploymentForScope(_ context.Context, _, scope string) (state.Deployment, error) {
	if f.lookupErr != nil {
		return state.Deployment{}, f.lookupErr
	}
	if !f.live || scope != api.DefaultEnvScope {
		return state.Deployment{}, state.ErrNotFound
	}
	return state.Deployment{ID: "live", Status: state.DeployLive}, nil
}

func (f releasePolicyFakeStore) StampSafeReleaseWorkerLease(context.Context, time.Duration) error {
	return nil
}

func (f releasePolicyFakeStore) SafeReleaseWorkerLeaseReady(context.Context) (bool, error) {
	return f.leaseOK, f.leaseErr
}

func TestApplyDefaultReleasePolicy(t *testing.T) {
	serving := releasePolicyFakeStore{live: true, leaseOK: true}
	falseVal, hundred := false, 100
	tests := []struct {
		name         string
		store        any
		app          state.App
		plan         api.Plan
		req          api.CreateDeploymentRequest
		want         releasePolicyOutcome
		wantCanary   string
		wantRollback *bool
	}{
		{name: "production release with a serving predecessor is safe", store: serving,
			want: releasePolicySafe, wantCanary: "balanced", wantRollback: ptrBool(true)},
		{name: "first deploy stays immediate", store: releasePolicyFakeStore{leaseOK: true}},
		{name: "immediate app policy opts out", store: serving,
			app: state.App{Manifest: state.AppManifest{ReleasePolicy: api.ReleasePolicyImmediate}}},
		{name: "preview app stays immediate", store: serving, app: state.App{PreviewOfSlug: "api"}},
		{name: "service app keeps scheduler rollout", store: serving,
			app: state.App{Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService}}},
		{name: "named environment scope stays immediate", store: serving, req: api.CreateDeploymentRequest{Scope: "staging"}},
		{name: "explicit canary none keeps rollback default", store: serving,
			req:  api.CreateDeploymentRequest{Canary: &api.CanaryPresetSpec{Preset: "none"}},
			want: releasePolicySafe, wantCanary: "none", wantRollback: ptrBool(true)},
		{name: "explicit traffic percent is not replaced", store: serving,
			req:  api.CreateDeploymentRequest{TrafficPercent: &hundred},
			want: releasePolicySafe, wantRollback: ptrBool(true)},
		{name: "explicit rollback false still gets the canary", store: serving,
			req:  api.CreateDeploymentRequest{RollbackOn5xx: &falseVal},
			want: releasePolicySafe, wantCanary: "balanced", wantRollback: ptrBool(false)},
		{name: "fully explicit request is untouched", store: serving,
			req:          api.CreateDeploymentRequest{RollbackOn5xx: &falseVal, Canary: &api.CanaryPresetSpec{Preset: "slow"}},
			wantCanary:   "slow",
			wantRollback: ptrBool(false)},
		{name: "canary worker down deploys immediately with rollback", store: releasePolicyFakeStore{live: true},
			want: releasePolicySafeUnavailable, wantRollback: ptrBool(true)},
		{name: "lease read error deploys immediately with rollback", store: releasePolicyFakeStore{live: true, leaseErr: errors.New("db down")},
			want: releasePolicySafeUnavailable, wantRollback: ptrBool(true)},
		{name: "predecessor lookup error fails open", store: releasePolicyFakeStore{lookupErr: errors.New("db down"), leaseOK: true}},
		{name: "store without lookup is legacy", store: struct{}{}},
		{name: "plan gates closed fill nothing they would reject", store: serving, plan: api.Plan("unknown"),
			want: releasePolicySafe},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			plan := tc.plan
			if plan == "" {
				plan = api.PlanFree
			}
			got := applyDefaultReleasePolicy(context.Background(), tc.store, slog.Default(), tc.app, plan, &req)
			if got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
			gotCanary := ""
			if req.Canary != nil {
				gotCanary = req.Canary.Preset
			}
			if gotCanary != tc.wantCanary {
				t.Fatalf("canary = %q, want %q", gotCanary, tc.wantCanary)
			}
			if (req.RollbackOn5xx == nil) != (tc.wantRollback == nil) ||
				(req.RollbackOn5xx != nil && *req.RollbackOn5xx != *tc.wantRollback) {
				t.Fatalf("rollback_on_5xx = %v, want %v", req.RollbackOn5xx, tc.wantRollback)
			}
		})
	}
}

func TestApplyDefaultReleasePolicyOperatorKillSwitch(t *testing.T) {
	t.Setenv(safeReleaseDefaultEnabledEnv, "false")
	var req api.CreateDeploymentRequest
	store := releasePolicyFakeStore{live: true, leaseOK: true}
	if got := applyDefaultReleasePolicy(context.Background(), store, slog.Default(), state.App{ID: "app"}, api.PlanFree, &req); got != releasePolicyNotApplied {
		t.Fatalf("outcome = %q, want not applied", got)
	}
	if req.Canary != nil || req.RollbackOn5xx != nil {
		t.Fatalf("request = %+v; want untouched", req)
	}
}

func TestApplyDefaultReleaseToEnqueue(t *testing.T) {
	var p apidsource.EnqueueParams
	got := applyDefaultReleaseToEnqueue(context.Background(), releasePolicyFakeStore{live: true, leaseOK: true}, slog.Default(), state.App{ID: "app"}, api.PlanFree, "", &p)
	if got != releasePolicySafe {
		t.Fatalf("outcome = %q, want safe", got)
	}
	if !p.RollbackOn5xx || p.CanaryPreset != "balanced" || p.CanaryTotalSteps == 0 || p.TrafficPercent != 1 || p.CanaryStepStartedAt == nil {
		t.Fatalf("enqueue params = %+v; want balanced canary at 1%% with rollback", p)
	}

	var first apidsource.EnqueueParams
	if got := applyDefaultReleaseToEnqueue(context.Background(), releasePolicyFakeStore{leaseOK: true}, slog.Default(), state.App{ID: "app"}, api.PlanFree, "", &first); got != releasePolicyNotApplied {
		t.Fatalf("first push outcome = %q, want not applied", got)
	}
	if first.RollbackOn5xx || first.CanaryPreset != "" || first.TrafficPercent != 0 {
		t.Fatalf("first push params = %+v; want untouched", first)
	}
}
