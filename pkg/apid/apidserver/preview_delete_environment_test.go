package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// DELETE /v1/apps/{slug} on a PR preview removed that one workload, left its
// siblings serving to the end of their lease and left it restorable. It now
// tears down the whole PR environment, and a torn-down preview is not
// restorable: restored, it served its PR code forever outside the quota.
func TestDeleteApp_PreviewTearsDownEnvironment(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "delete-env"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	preview := func(slug, parent, workload string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: slug, WorkloadName: workload,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: parent,
			PreviewPrNumber: 4, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
		return app
	}
	root := preview("pr-4-delete-web", "delete-web", "web")
	sibling := preview("pr-4-delete-worker", "delete-worker", "worker")

	if rec := e.Do(t, "DELETE", "/v1/apps/"+root.Slug, nil, nil); rec.Code != 204 {
		t.Fatalf("delete preview status %d: %s", rec.Code, rec.Body)
	}
	for _, app := range []state.App{root, sibling} {
		got, err := store.AppByID(ctx, app.ID)
		if err != nil || got.Status != state.AppDeleted || got.PreviewPrState != state.PreviewPrStateTornDown {
			t.Fatalf("%s after delete = (%q, %q, %v), want deleted and torn_down", app.Slug, got.Status, got.PreviewPrState, err)
		}
	}
	if rec := e.Do(t, "POST", "/v1/apps/"+root.Slug+"/restore", nil, nil); rec.Code != 409 {
		t.Fatalf("restore torn-down preview status %d: %s, want 409", rec.Code, rec.Body)
	}
}
