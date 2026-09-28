package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDestroyDevSessionEmptiesAndDeletesAttachedBucket(t *testing.T) {
	e := setup(t, api.PlanHobby)
	setS3Flag(t, e, true)
	provider := &fakeObjectProvider{objects: map[string]bool{"reports/result.csv": true}}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	const workspaceID = "22222222222222222222222222222222"
	created := e.do(t, http.MethodPut, "/v1/dev/sessions/export-api", api.UpsertDevSessionRequest{WorkspaceID: workspaceID}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("developer session = %d: %s", created.Code, created.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	bucketPath := "/v1/apps/" + session.App.Slug + "/buckets"
	bucketResponse(t, e.do(t, http.MethodPost, bucketPath, api.CreateObjectBucketRequest{Name: "exports"}, nil), http.StatusCreated)
	destroyed := e.do(t, http.MethodDelete, "/v1/dev/sessions/export-api?workspace_id="+workspaceID, nil, nil)
	if destroyed.Code != http.StatusNoContent {
		t.Fatalf("destroy developer session = %d: %s", destroyed.Code, destroyed.Body.String())
	}
	if len(provider.objects) != 0 {
		t.Fatalf("provider objects left behind: %v", provider.objects)
	}
	app, err := e.store.AppByID(t.Context(), session.App.ID)
	if err != nil || app.Status != state.AppDeleted {
		t.Fatalf("app status = %q, err = %v", app.Status, err)
	}
	buckets, err := e.store.ListObjectBuckets(t.Context(), e.acct.ID, session.App.ID)
	if err != nil || len(buckets) != 0 {
		t.Fatalf("buckets left behind: %v, err = %v", buckets, err)
	}

	// The lease janitor must reach the same cleanup when the CLI vanishes.
	provider.objects["reports/after-crash.csv"] = true
	const lostWorkspaceID = "33333333333333333333333333333333"
	created = e.do(t, http.MethodPut, "/v1/dev/sessions/export-api", api.UpsertDevSessionRequest{WorkspaceID: lostWorkspaceID}, nil)
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		t.Fatalf("second developer session = %d: %s", created.Code, created.Body.String())
	}
	bucketPath = "/v1/apps/" + session.App.Slug + "/buckets"
	bucketResponse(t, e.do(t, http.MethodPost, bucketPath, api.CreateObjectBucketRequest{Name: "exports"}, nil), http.StatusCreated)
	janitor := newPreviewJanitor(e.store, noopNotifier{}, nil, testLogger(), true).
		withClock(func() time.Time { return time.Now().Add(25 * time.Hour) }).
		withResourceCleanup(e.s.cleanupDevSessionResources)
	if err := janitor.sweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.objects) != 0 {
		t.Fatalf("lease cleanup left objects: %v", provider.objects)
	}
	app, err = e.store.AppByID(t.Context(), session.App.ID)
	if err != nil || app.Status != state.AppDeleted {
		t.Fatalf("expired app status = %q, err = %v", app.Status, err)
	}
}
