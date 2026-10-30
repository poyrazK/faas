package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A preview's slug is its identity (pr-<N>-<app>). Renamed, the next PR head
// no longer found it and reserved a second preview while the renamed one held
// its slot until its lease ended.
func TestRenameApp_RefusesPreview(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "rename-parent")
	expires := time.Now().Add(time.Hour)
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: (*e.Acct()).ID, Slug: "pr-3-rename-parent", Type: state.AppTypeApp, RAMMB: 256,
		MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: "rename-parent", PreviewPrNumber: 3,
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("seed preview: %v", err)
	}
	rec := e.Do(t, "POST", "/v1/apps/pr-3-rename-parent/rename", api.RenameAppRequest{NewSlug: "renamed-preview"}, nil)
	if rec.Code != 409 {
		t.Fatalf("rename preview status %d: %s, want 409", rec.Code, rec.Body)
	}
	if _, err := (*e.Store()).AppBySlug(context.Background(), "pr-3-rename-parent"); err != nil {
		t.Fatalf("preview after refused rename: %v", err)
	}
}
