// adr: 581
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppUpdateInEnvironmentChangesOnlyDesiredStageSettings(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	ram, concurrency := 256, 2
	app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram, MaxConcurrency: &concurrency})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPatch, "/v1/apps/"+app.Slug+"?environment=staging", app.Slug,
		[]byte(`{"ram_mb":512,"maintenance_mode":true,"request_timeout_s":17,"app_protocol":"http2"}`))
	req.Header.Set("If-Workload-Revision", "0")
	srv.updateApp(rec, req, account)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Gregale-Workload-Revision") != "1" {
		t.Fatalf("stage patch = %d, %s", rec.Code, rec.Body.String())
	}
	var response api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RAMMB != 512 || !response.MaintenanceMode || response.Manifest.RequestTimeoutS != 17 || response.AppProtocol != "http2" {
		t.Fatalf("stage response = %+v", response)
	}
	production, err := store.AppByID(ctx, app.ID)
	if err != nil || production.RAMMB != 256 || production.MaintenanceMode || production.Manifest.RequestTimeoutS != 0 {
		t.Fatalf("stage patch changed production: %+v, %v", production, err)
	}
	read, readRec := projectRequest(http.MethodGet, "/v1/apps/"+app.Slug+"?environment=staging", app.Slug, nil)
	srv.getApp(readRec, read, account)
	if readRec.Code != http.StatusOK || readRec.Header().Get("X-Gregale-Workload-Revision") != "1" || readRec.Header().Get("X-Gregale-Workload-Config-Hash") != rec.Header().Get("X-Gregale-Workload-Config-Hash") {
		t.Fatalf("stage read = %d, %s", readRec.Code, readRec.Body.String())
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &response); err != nil || response.RAMMB != 512 {
		t.Fatalf("stage read did not select desired settings: RAM=%d, err=%v", response.RAMMB, err)
	}
	stale, staleRec := projectRequest(http.MethodPatch, "/v1/apps/"+app.Slug+"?environment=staging", app.Slug, []byte(`{"ram_mb":256}`))
	stale.Header.Set("If-Workload-Revision", "0")
	srv.updateApp(staleRec, stale, account)
	if staleRec.Code != http.StatusConflict {
		t.Fatalf("stale update = %d, %s", staleRec.Code, staleRec.Body.String())
	}
	protected, protectedRec := projectRequest(http.MethodPatch, "/v1/apps/"+app.Slug+"?environment=production", app.Slug, []byte(`{"ram_mb":256}`))
	srv.updateApp(protectedRec, protected, account)
	if protectedRec.Code != http.StatusConflict {
		t.Fatalf("protected environment update = %d, %s", protectedRec.Code, protectedRec.Body.String())
	}
}
