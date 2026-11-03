package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplyDeploymentEnvironmentResolvesRegisteredProjectEnvironment(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}

	req := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, app, req); problem != nil {
		t.Fatalf("applyDeploymentEnvironment: %v", problem)
	}
	if req.Scope != "staging" {
		t.Fatalf("resolved scope = %q, want staging", req.Scope)
	}
}

func TestApplyDeploymentEnvironmentRejectsUnknownOrStandaloneTarget(t *testing.T) {
	srv, store, acct, _, projectApp := apidserver.NewProjectLifecycleFixture(t)

	unknown := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, projectApp, unknown); problem == nil || problem.Status != 404 {
		t.Fatalf("unknown environment problem = %+v, want 404", problem)
	}

	standalone, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "standalone", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	request := &api.CreateDeploymentRequest{Environment: "production"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, standalone, request); problem == nil || problem.Status != 400 {
		t.Fatalf("standalone environment problem = %+v, want 400", problem)
	}
}

func TestValidateProjectDeploymentEnvironmentUsesProjectRegistry(t *testing.T) {
	srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, project.Slug, "staging"); problem != nil {
		t.Fatalf("registered environment rejected: %v", problem)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, project.Slug, "testing"); problem == nil || problem.Status != 404 {
		t.Fatalf("unknown environment problem = %+v, want 404", problem)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, "new-project", "production"); problem != nil {
		t.Fatalf("seeded production environment for new project rejected: %v", problem)
	}
}

// A protected environment other than production takes releases through an
// approved plan or promotion: a direct deploy, by environment or raw scope,
// is refused unless it stages the release without traffic, and that staged
// release cannot take traffic by hand. Production keeps direct releases
// (ADR-20261019071733 amendment 9).
func TestProtectedEnvironmentRefusesDirectReleases(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "preprod", Protected: true,
	}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]*api.CreateDeploymentRequest{
		"environment": {Environment: "preprod"},
		"raw scope":   {Scope: "preprod"},
	} {
		if p := srv.ApplyDeploymentEnvironment(ctx, acct, app, req); p == nil || p.Code != api.CodeProjectEnvironmentApprovalRequired {
			t.Fatalf("%s deploy into a protected environment = %+v, want project_environment_approval_required", name, p)
		}
	}
	zero := 0
	staged := &api.CreateDeploymentRequest{Environment: "preprod", TrafficPercent: &zero}
	if p := srv.ApplyDeploymentEnvironment(ctx, acct, app, staged); p != nil {
		t.Fatalf("staging without traffic = %+v, want it allowed", p)
	}
	if p := srv.ApplyDeploymentEnvironment(ctx, acct, app, &api.CreateDeploymentRequest{Environment: "production"}); p != nil {
		t.Fatalf("a production deploy = %+v, want it held by the release guards instead", p)
	}

	dark, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "preprod", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if p := srv.ApprovalTrafficProblemForTest(ctx, app, dark); p == nil || p.Code != api.CodeProjectEnvironmentApprovalRequired {
		t.Fatalf("giving a staged release traffic in a protected environment = %+v", p)
	}
}

// Publishing a release set serves its members at once, so they meet the
// release guards an activation does (ADR-20261019071733 amendment 10).
func TestPublishedReleaseSetMembersMeetTheReleaseGuards(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "preprod", Protected: true,
	}); err != nil {
		t.Fatal(err)
	}
	dark := func(scope string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage,
			Status: state.DeployLive, TrafficPercent: 0, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	members := func(d state.Deployment) []state.ProjectReleaseMember {
		return []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}}
	}
	if p := srv.ReleaseSetMembersGuardProblemForTest(ctx, "preprod", members(dark("preprod"))); p == nil || p.Code != api.CodeProjectEnvironmentApprovalRequired {
		t.Fatalf("publishing a staged release in a protected environment = %+v", p)
	}
	production := dark("production")
	if p := srv.ReleaseSetMembersGuardProblemForTest(ctx, "production", members(production)); p != nil {
		t.Fatalf("publishing an unguarded staged production release = %+v", p)
	}
	if _, err := store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID,
		RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if p := srv.ReleaseSetMembersGuardProblemForTest(ctx, "production", members(production)); p == nil || p.Code != api.CodeReleaseApprovalCanaryRequired {
		t.Fatalf("publishing a staged release of an app requiring approvals = %+v", p)
	}
	bad := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: "not-a-uuid"}}
	if p := srv.ReleaseSetMembersGuardProblemForTest(ctx, "production", bad); p == nil || p.Code != api.CodeValidation {
		t.Fatalf("an invalid member = %+v, want validation", p)
	}
}
