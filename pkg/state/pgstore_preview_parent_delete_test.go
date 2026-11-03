package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// #5756 item 2: `gregale apps rm` refused an app while it had a live PR
// preview, answering traffic_policy_too_complex ("The app binding change
// could not be verified within platform analysis limits"). It succeeded once
// the preview was destroyed, so a parent whose preview outlived its PR could
// not be deleted at all.
func TestPg_ScheduleAppDeletionWithLivePreview(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "preview-parent-delete@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	parent, err := s.CreateAppIfUnderQuota(ctx, state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp,
		RAMMB: 256, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP}, limits)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := s.CreateDeployment(ctx, state.Deployment{AppID: parent.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, RootfsPath: "/artifact"}); err != nil {
		t.Fatalf("parent deployment: %v", err)
	}
	expiry := time.Now().Add(time.Hour)
	preview, err := s.CreateAppIfUnderQuota(ctx, state.App{AccountID: acct.ID, Slug: "pr-3-shop", Type: state.AppTypeApp,
		RAMMB: 256, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP,
		PreviewOfSlug: "shop", PreviewPrNumber: 3, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry}, limits)
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}
	if _, err := s.CreateDeployment(ctx, state.Deployment{AppID: preview.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, RootfsPath: "/artifact"}); err != nil {
		t.Fatalf("preview deployment: %v", err)
	}
	deleted, err := s.ScheduleAppDeletion(ctx, parent.ID, time.Time{})
	if err != nil {
		t.Fatalf("delete an app with a live preview: %v", err)
	}
	if deleted.Status != state.AppDeleted {
		t.Fatalf("parent status = %q, want deleted", deleted.Status)
	}
}
