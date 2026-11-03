package githubd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A production app's declared-route contract answers undeclared paths at the
// gateway. A PR preview without it served every path on its public host,
// including the ones production hides.
func TestHandlePullRequest_PreviewKeepsDeclaredRouteContract(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	on := true
	routes := []state.DeclaredRoute{{Path: "/api/items", Methods: []string{"GET"}}}
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{
		OnlyAllowDeclaredRoutes: &on, SetOnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: &routes, SetDeclaredRoutes: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.OnlyAllowDeclaredRoutes || len(preview.DeclaredRoutes) != 1 || preview.DeclaredRoutes[0].Path != "/api/items" {
		t.Fatalf("preview contract = (%t, %+v), want production's", preview.OnlyAllowDeclaredRoutes, preview.DeclaredRoutes)
	}
}

// Turning previews off made githubd ignore the PR's pushes while its open
// preview kept serving the last commit to the end of its lease. The next
// event retires it instead.
func TestHandlePullRequest_PreviewsOffRetiresOpenPreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	neutral := recordNeutralChecks(svc)
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.PreviewEnabled = false
	if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	result, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40)))
	if !IsIgnored(err) || !result.WasIgnored {
		t.Fatalf("push with previews off = (%+v, %v), want ignored", result, err)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil || preview.PreviewPrState != state.PreviewPrStateClosed ||
		preview.PreviewExpiresAt == nil || preview.PreviewExpiresAt.After(time.Now()) {
		t.Fatalf("preview after previews off = (%+v, %v), want its lease ended", preview, err)
	}
	if len(*neutral) != 1 || (*neutral)[0].title != "Previews off" {
		t.Fatalf("neutral checks = %+v, want one saying previews are off", *neutral)
	}
	// The next push finds nothing to retire and posts nothing.
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("c", 40))); !IsIgnored(err) {
		t.Fatalf("second push with previews off = %v, want ignored", err)
	}
	if len(*neutral) != 1 {
		t.Fatalf("neutral checks after a second push = %+v, want still one", *neutral)
	}
}
