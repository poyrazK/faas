package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — amendment 14 (release review rounds 12-15).

// Flags change what an environment's code does: a protected environment
// other than production takes them from an approved plan, like its other
// configuration (ADR-20261019071733 amendment 12). Production keeps direct
// edits.
func TestFeatureFlagWritesRefusedInAProtectedEnvironment(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	store := *e.Store()
	acct := *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "flagged", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging", Protected: true}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"expected_version": 0, "config": map[string]any{"flags": []any{}}}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/projects/flagged/environments/staging/flags", body, nil),
		http.StatusConflict, api.CodeConflict)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/projects/flagged/environments/staging/flags/rollback",
		map[string]any{"expected_version": 1, "version": 1}, nil), http.StatusConflict, api.CodeConflict)
	if rec := e.Do(t, http.MethodPut, "/v1/projects/flagged/environments/production/flags", body, nil); rec.Code == http.StatusConflict {
		t.Fatalf("production flag write = %d %s, want it allowed", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/projects/flagged/environments/staging/flags", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("protected flag read = %d %s, want 200", rec.Code, rec.Body)
	}
}

// A release that failed, was cancelled or superseded ships nothing: a
// decision on it is refused rather than announced as approved.
func TestReleaseApprovalRefusedOnAReleaseThatEnded(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-ended", time.Now(), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	store := *f.e.Store()
	ended, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: f.app.ID, ImageDigest: "sha256:ended", Status: state.DeploySnapshotting,
		CreatedAt: time.Now().UTC().Add(2 * time.Second), DeployedByUserID: (*f.e.Acct()).ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentFailed(ctx, ended.ID, "build_failed", "the build failed"); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, sessionDo(t, f.e, http.MethodPost, "/v1/deployments/"+ended.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove}, nil), http.StatusConflict, api.CodeReleaseApprovalDecided)
}

// An app that requires approvals cannot switch to the immediate release
// policy: its releases would have no canary to wait in.
func TestImmediateReleasePolicyRefusedWhileApprovalsAreRequired(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "approval-immediate", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
		AppID: app.ID, AccountID: app.AccountID, RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60,
		Approvers: state.ReleaseApproversAdmin, UpdatedBy: "owner@example.com",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	immediate := api.ReleasePolicyImmediate
	apidserver.AssertProblem(t, e.Do(t, http.MethodPatch, "/v1/apps/approval-immediate", api.UpdateAppRequest{ReleasePolicy: &immediate}, nil),
		http.StatusConflict, api.CodeConflict)
	if _, err := store.DeleteReleaseApprovalPolicy(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/approval-immediate", api.UpdateAppRequest{ReleasePolicy: &immediate}, nil); rec.Code != http.StatusOK {
		t.Fatalf("immediate without a policy = %d %s, want 200", rec.Code, rec.Body)
	}
}

// A production release of an app with release gates cannot turn off both of
// the gates' judges: the canary and the post-release watch.
func TestReleaseGatesNeedAJudgedRelease(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "gated"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateReleaseGate(ctx, state.ReleaseGate{AccountID: app.AccountID, AppID: app.ID, Name: "no-new-errors", Kind: "new_errors", MinEvents: 3, CreatedBy: "ops@example.com"}, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	off, on, none := false, true, &api.CanaryPresetSpec{Preset: "none"}
	srv := *e.S()
	for _, tc := range []struct {
		name    string
		req     api.CreateDeploymentRequest
		refused bool
	}{
		{name: "no canary and no watch", req: api.CreateDeploymentRequest{Canary: none, RollbackOn5xx: &off}, refused: true},
		{name: "production environment", req: api.CreateDeploymentRequest{Environment: "production", Canary: none, RollbackOn5xx: &off}, refused: true},
		{name: "watched", req: api.CreateDeploymentRequest{Canary: none, RollbackOn5xx: &on}},
		{name: "default watch", req: api.CreateDeploymentRequest{Canary: none}},
		{name: "canary", req: api.CreateDeploymentRequest{Canary: &api.CanaryPresetSpec{Preset: "balanced"}, RollbackOn5xx: &off}},
		{name: "staging", req: api.CreateDeploymentRequest{Environment: "staging", Canary: none, RollbackOn5xx: &off}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			problem := srv.ReleaseApprovalCanaryProblemForTest(ctx, app, &req)
			if (problem != nil) != tc.refused {
				t.Fatalf("problem = %+v, refused want %v", problem, tc.refused)
			}
		})
	}
}
