package githubd

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A project deploy refuses a service whose declared memory exceeds the plan;
// the PR preview path skipped that admission and booted the preview with the
// head's RAM, on any plan.
func TestHandlePullRequest_PreviewRefusesHeadAbovePlan(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, rec := newSourcePreviewService(t, rig, []reposcan.Workload{
		{Name: "api", Resources: &api.ComposeResources{MemoryMB: 1 << 20}},
	})
	result, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if !IsIgnored(err) || len(result.Added) != 0 {
		t.Fatalf("head above the plan = (%+v, %v), want a reported setup failure", result, err)
	}
	if _, err := rig.mem.AppBySlug(ctx, "pr-42-demo-app"); err == nil {
		t.Fatal("a head above the plan reserved a preview")
	}
	reported := false
	for _, check := range rec.checks {
		reported = reported || strings.Contains(check.summary, "MB per app")
	}
	if !reported {
		t.Fatalf("checks = %+v, want the plan's RAM limit explained on the PR", rec.checks)
	}
}

// A preview keeps the production app's tenant-only gate and its security
// posture policy, and every head refreshes them: the head never sets them.
func TestHandlePullRequest_PreviewKeepsTenantAndSecurityGates(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	on, enforce := true, api.AppSecurityPolicyEnforce
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{
		PlatformTenantRequired: &on, SetPlatformTenantRequired: true, SecurityPolicy: &enforce, SetSecurityPolicy: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api", DependsOn: []string{"admin"}}, {Name: "admin"}})
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil || len(opened.Added) == 0 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	for _, slug := range []string{"pr-42-demo-app", "pr-42-admin"} {
		preview, err := rig.mem.AppBySlug(ctx, slug)
		if err != nil || !preview.PlatformTenantRequired || preview.SecurityPolicy != api.AppSecurityPolicyEnforce {
			t.Fatalf("%s = tenant %t, policy %q (%v); want production's", slug, preview.PlatformTenantRequired, preview.SecurityPolicy, err)
		}
	}

	off := false
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{
		PlatformTenantRequired: &off, SetPlatformTenantRequired: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("push: %v", err)
	}
	if preview, err := rig.mem.AppBySlug(ctx, "pr-42-demo-app"); err != nil || preview.PlatformTenantRequired {
		t.Fatalf("preview after production dropped the gate = (%t, %v), want it dropped", preview.PlatformTenantRequired, err)
	}
}
