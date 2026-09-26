package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectReleaseInventoryAndEnvironmentState(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), account state.Account, query, id string) *httptest.ResponseRecorder {
		t.Helper()
		req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/release-sets"+query, "shop", nil)
		req.SetPathValue("environment", "production")
		req.SetPathValue("release", id)
		handler(rec, req, account)
		return rec
	}
	if rec := call(srv.getActiveProjectReleaseSet, acct, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("empty active: %d %s", rec.Code, rec.Body.String())
	}
	var published []state.ProjectReleaseSet
	var latest state.Deployment
	for i := 0; i < 3; i++ {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		latest = dep
		if i == 2 {
			break
		}
		release, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		published = append(published, release)
	}
	var page api.ProjectReleaseSetListResponse
	rec := call(srv.listProjectReleaseSets, acct, "?limit=1", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != published[1].ID || page.NextBefore == "" {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv.listProjectReleaseSets, acct, "?limit=1&before="+url.QueryEscape(page.NextBefore), "")
	page = api.ProjectReleaseSetListResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != published[0].ID || page.NextBefore != "" {
		t.Fatalf("next: %d %s", rec.Code, rec.Body.String())
	}
	var active api.ProjectReleaseSetResponse
	rec = call(srv.getActiveProjectReleaseSet, acct, "", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &active) != nil || active.ID != published[1].ID {
		t.Fatalf("active: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv.getProjectReleaseSet, acct, "", published[0].ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("retired graph: %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=invalid", "?before=broken"} {
		if rec := call(srv.listProjectReleaseSets, acct, query, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
	if rec := call(srv.getProjectReleaseSet, acct, "", "invalid"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid UUID: %d", rec.Code)
	}
	foreign := state.Account{ID: uuid.NewString()}
	for _, handler := range []func(http.ResponseWriter, *http.Request, state.Account){srv.listProjectReleaseSets, srv.getActiveProjectReleaseSet, srv.getProjectReleaseSet} {
		if rec := call(handler, foreign, "", published[0].ID); rec.Code != http.StatusNotFound {
			t.Fatalf("foreign read: %d %s", rec.Code, rec.Body.String())
		}
	}
	snapshot, problem := srv.loadProjectEnvironmentState(ctx, acct, project.Slug, "production")
	if problem != nil || snapshot.ReleaseSetStatus != "active" || snapshot.ActiveReleaseSet == nil || snapshot.ActiveReleaseSet.ID != published[1].ID {
		t.Fatalf("state graph: %+v %v", snapshot, problem)
	}
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].AppID != app.ID || snapshot.Workloads[0].Release.DeploymentID != latest.ID {
		t.Fatalf("existing release semantics changed: %+v", snapshot.Workloads)
	}
}

type failedReleaseReader struct{ *state.MemStore }

func (s failedReleaseReader) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return state.ProjectReleaseSet{}, errors.New("database unavailable")
}

func TestEnvironmentStateDoesNotHideReleaseReadFailure(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	srv.store = failedReleaseReader{store}
	if _, problem := srv.loadProjectEnvironmentState(context.Background(), acct, project.Slug, "production"); problem == nil {
		t.Fatal("failed release read was represented as no active release")
	}
}
