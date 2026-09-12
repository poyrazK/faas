package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObsIncidents_EmptyResponseIsStable(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, http.MethodGet, "/v1/admin/obs/incidents", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Items == nil {
		t.Fatal("items must be a non-nil array")
	}
	if response.Limit != api.ObsIncidentLimitDefault {
		t.Fatalf("limit = %d, want %d", response.Limit, api.ObsIncidentLimitDefault)
	}
	if response.Since.IsZero() || response.GeneratedAt.IsZero() {
		t.Fatal("generated_at and since must be populated")
	}
}

func TestObsIncidents_ComposesSourcesAndPaginates(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "incident-app", Type: state.AppTypeApp})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	old := time.Now().UTC().Add(-2 * time.Minute)
	failed, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Status: state.DeployFailed, CreatedAt: old, ErrorCode: api.CodeStageImageBuildOOM,
	})
	if err != nil {
		t.Fatalf("CreateDeployment failed: %v", err)
	}
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Status: state.DeployBuilding, CreatedAt: old.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateDeployment building: %v", err)
	}
	job, err := e.store.JobCreate(ctx, e.acct.ID, "incident-job", "app", "", nil, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if _, _, err := e.store.JobRunCreate(ctx, job.ID, e.acct.ID, "manual", nil, nil, nil, nil, 1); err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	if _, err := e.store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
		ID: uuid.NewString(), Name: "incident-node", Lifecycle: state.NodeLifecycleActive,
		LastHeartbeatAt: time.Now().UTC().Add(-2 * state.DefaultHeartbeatStaleness),
	}); err != nil {
		t.Fatalf("UpsertComputeNodeFromOperator: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/admin/obs/incidents?limit=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var first api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor=%q; want 2 items and a cursor", len(first.Items), first.NextCursor)
	}
	key, err := cursor.Decode(first.NextCursor)
	if err != nil || key.ID == "" {
		t.Fatalf("decode next cursor: key=%+v err=%v", key, err)
	}

	rec = e.do(t, http.MethodGet, "/v1/admin/obs/incidents?limit=2&cursor="+first.NextCursor, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var second api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.Items) == 0 || second.NextCursor != "" {
		t.Fatalf("second page = %d items, cursor=%q; want remaining items and no cursor", len(second.Items), second.NextCursor)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		seen[item.Type] = true
		if item.ID == "deployment/"+failed.ID && item.Severity != "error" {
			t.Fatalf("failed deployment severity = %q, want error", item.Severity)
		}
		if item.DedupeKey == "" || item.ResourceID == "" {
			t.Fatalf("incident missing stable identity: %+v", item)
		}
	}
	for _, kind := range []string{"deployment", "job_run", "compute_node"} {
		if !seen[kind] {
			t.Errorf("incident types missing %q: seen=%v", kind, seen)
		}
	}
}

func TestObsIncidents_RejectsInvalidFiltersAndCursor(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	for _, path := range []string{
		"/v1/admin/obs/incidents?type=unknown",
		"/v1/admin/obs/incidents?severity=urgent",
		"/v1/admin/obs/incidents?since=not-a-time",
		"/v1/admin/obs/incidents?cursor=not-a-cursor",
	} {
		rec := e.do(t, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400; body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestObsIncidents_RequiresAdminScope(t *testing.T) {
	e := newObsEnv(t, []string{api.ScopeAppsRead}, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, http.MethodGet, "/v1/admin/obs/incidents", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}
