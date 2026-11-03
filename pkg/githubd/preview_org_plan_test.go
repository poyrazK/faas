package githubd

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// newOrgPreviewRig is newPreviewRig with the project and its production app
// owned by an organization on orgPlan. The binding creator stays on the
// rig's personal Hobby plan.
func newOrgPreviewRig(t *testing.T, orgPlan api.Plan) *previewTestRig {
	t.Helper()
	ctx := context.Background()
	rig := newRig(t, nil)
	org, err := rig.mem.CreateOrg(ctx, state.Org{Slug: "preview-company", Name: "Preview Company", Plan: orgPlan})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.mem.AddOrgMember(ctx, org.ID, rig.acct, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := rig.mem.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_preview_company"); err != nil {
		t.Fatal(err)
	}
	if err := rig.mem.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_preview_company"); err != nil {
		t.Fatal(err)
	}
	project, err := rig.mem.CreateProject(ctx, state.Project{AccountID: rig.acct, OrgID: org.ID, Slug: "demo-project"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := rig.mem.CreateApp(ctx, state.App{
		AccountID: rig.acct, OrgID: org.ID, Slug: "demo-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
		IdleTimeoutS: 30, Status: state.AppActive, ProjectID: project.ID, WorkloadName: "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := state.AppResourceAccount(ctx, rig.mem, parent)
	if err != nil || owner.Plan != orgPlan {
		t.Fatalf("org app resource account = %+v (%v), want the %s organization", owner, err, orgPlan)
	}
	return &previewTestRig{testRig: rig, parentSlug: "demo-app", parentID: parent.ID, parentProjectID: project.ID, parentWorkloadName: "api"}
}

// Bug hunt 33: an organization project's previews were admitted against the
// binding creator's personal plan, while a deploy of the same head uses the
// organization's plan.
func TestHandlePullRequest_OrgPreviewUsesTheOrganizationPlan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		orgPlan  api.Plan
		memoryMB int
		admitted bool
	}{
		// Hobby (the creator's plan) allows 512 MB per app; Pro allows 1024.
		{"pro organization admits what the creator's hobby plan would refuse", api.PlanPro, 768, true},
		// Free allows 256 MB per app.
		{"free organization refuses what the creator's hobby plan would admit", api.PlanFree, 384, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rig := newOrgPreviewRig(t, tc.orgPlan)
			svc, rec := newSourcePreviewService(t, rig, []reposcan.Workload{
				{Name: "api", Resources: &api.ComposeResources{MemoryMB: tc.memoryMB}},
			})
			_, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
			_, lookupErr := rig.mem.AppBySlug(ctx, "pr-42-demo-app")
			refused := false
			for _, check := range rec.checks {
				refused = refused || strings.Contains(check.summary, "MB per app")
			}
			if tc.admitted && (lookupErr != nil || refused) {
				t.Fatalf("preview = %v (handle err %v, refused %t, checks %+v); want admitted under the %s organization", lookupErr, err, refused, rec.checks, tc.orgPlan)
			}
			if !tc.admitted && (lookupErr == nil || !refused) {
				t.Fatalf("preview lookup = %v, refused %t (checks %+v); want refused under the %s organization", lookupErr, refused, rec.checks, tc.orgPlan)
			}
		})
	}
}
