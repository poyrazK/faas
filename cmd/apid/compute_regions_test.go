package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestComputeRegionsAndAppCreation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	region := "eu-fsn"
	if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: region, DisplayName: "Falkenstein", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "regional", TargetURL: "unix:///regional", AdmissionCeilingMB: 1024, VCPUBudget: 160, Active: true, Region: &region}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/regions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("regions: %d %s", rec.Code, rec.Body)
	}
	var regions []api.ComputeRegionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &regions); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range regions {
		if item.ID == region && item.ActiveNodes == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("region missing: %+v", regions)
	}
	for _, tc := range []struct {
		slug, region string
		status       int
	}{
		{"regional-api", region, http.StatusCreated},
		{"unknown-region", "unknown", http.StatusUnprocessableEntity},
		{"invalid-region", "EU FSN", http.StatusBadRequest},
	} {
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: tc.slug, ComputeRegion: tc.region}, nil)
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.slug, rec.Code, rec.Body)
		}
		if rec.Code == http.StatusCreated {
			var app api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
				t.Fatal(err)
			}
			if app.ComputeRegion != region {
				t.Fatalf("compute region=%q", app.ComputeRegion)
			}
		}
	}
}

func TestComputeRegionCreationRequiresOperatorGate(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "regional-disabled", ComputeRegion: "eu-fsn"}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled region creation: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "legacy-app"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("legacy creation: %d %s", rec.Code, rec.Body)
	}
}

func TestComputeRegionPreviewInheritsParent(t *testing.T) {
	preview := previewAppFromParent(state.App{ComputeRegion: "eu-fsn", Slug: "parent"}, "pr-1-parent", 1, time.Now())
	if preview.ComputeRegion != "eu-fsn" {
		t.Fatalf("preview region = %q", preview.ComputeRegion)
	}
}

func TestComputeRegionCatalogSurvivesOutageAndPause(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	region := state.ComputeRegion{ID: "eu-fsn", DisplayName: "Falkenstein", AcceptingNewApps: true}
	if _, err := e.store.UpsertComputeRegion(t.Context(), region); err != nil {
		t.Fatal(err)
	}
	label := region.ID
	node, err := e.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "regional", TargetURL: "unix:///regional", AdmissionCeilingMB: 1024, VCPUBudget: 160, Active: true, Region: &label, LastHeartbeatAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "eu-hel", DisplayName: "Helsinki", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	region.AcceptingNewApps = false
	if _, err := e.store.UpsertComputeRegion(t.Context(), region); err != nil {
		t.Fatal(err)
	}
	assertProblem := func(slug, id, code string) {
		t.Helper()
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug, ComputeRegion: id}, nil)
		var p api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if rec.Code != api.StatusForCode(code) || p.Code != code || p.ComputeRegion != id {
			t.Fatalf("response %d %+v", rec.Code, p)
		}
	}
	assertProblem("paused", region.ID, api.CodeComputeRegionAdmissionPaused)
	assertProblem("no-nodes", "eu-hel", api.CodeComputeRegionUnavailable)
	if err := e.store.SetComputeNodeActive(t.Context(), node.ID, false); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/regions", nil, nil)
	var regions []api.ComputeRegionResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &regions) != nil || len(regions) != 3 {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body)
	}
	if regions[0].ID != region.ID || regions[0].AcceptingNewApps || regions[0].Availability != "unavailable" || regions[0].RegisteredNodes != 1 || regions[0].EligibleNodes != 0 {
		t.Fatalf("lost unavailable region: %+v", regions)
	}
}

func TestComputeRegionObservedHealth(t *testing.T) {
	now := time.Now()
	label := "eu-fsn"
	base := state.ComputeNode{Active: true, Region: &label, VCPUBudget: 160, AdmissionCeilingMB: 1024, LastHeartbeatAt: now}
	stale, unknown, inactive := base, base, base
	stale.LastHeartbeatAt = now.Add(-2 * state.DefaultHeartbeatStaleness)
	unknown.LastHeartbeatAt = time.Time{}
	inactive.Active = false
	for _, tc := range []struct {
		name, health, availability string
		nodes                      []state.ComputeNode
		eligible, healthy          int
	}{
		{"fresh", "healthy", "available", []state.ComputeNode{base}, 1, 1},
		{"partial", "degraded", "available", []state.ComputeNode{base, stale}, 2, 1},
		{"unobserved", "unknown", "available", []state.ComputeNode{unknown}, 1, 0},
		{"stale", "unavailable", "available", []state.ComputeNode{stale}, 1, 0},
		{"inactive", "unavailable", "unavailable", []state.ComputeNode{inactive}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := computeRegionResponse(state.ComputeRegion{ID: label, DisplayName: "Falkenstein"}, tc.nodes, now)
			if got.ObservedHealth != tc.health || got.Availability != tc.availability || got.EligibleNodes != tc.eligible || got.HealthyNodes != tc.healthy || got.AcceptingNewApps {
				t.Fatalf("observations=%+v", got)
			}
		})
	}
}

func TestComputeRegionPolicyRequiresOperatorAndExplicitBoolean(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    bool
		status     int
	}{
		{"set", `{"display_name":"Falkenstein","accepting_new_apps":false}`, true, http.StatusOK},
		{"missing_boolean", `{"display_name":"Falkenstein"}`, true, http.StatusBadRequest},
		{"unknown_field", `{"display_name":"Falkenstein","accepting_new_apps":true,"typo":1}`, true, http.StatusBadRequest},
		{"trailing_json", `{"display_name":"Falkenstein","accepting_new_apps":true} {}`, true, http.StatusBadRequest},
		{"non_operator", `{"display_name":"Falkenstein","accepting_new_apps":true}`, false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, cookie := newForceHarness(t, nil)
			if !tc.allowed {
				srv.adminAllowlist.emails = map[string]struct{}{}
			}
			req := httptest.NewRequest(http.MethodPut, "/v1/regions/eu-fsn", strings.NewReader(tc.body))
			req.AddCookie(cookie)
			req.Header.Set("Idempotency-Key", "regional-policy-test")
			rec := httptest.NewRecorder()
			srv.handler().ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if tc.status == http.StatusOK {
				region, err := store.ComputeRegionByID(t.Context(), "eu-fsn")
				if err != nil || region.AcceptingNewApps {
					t.Fatalf("policy %+v %v", region, err)
				}
			} else if _, err := store.ComputeRegionByID(t.Context(), "eu-fsn"); err == nil {
				t.Fatal("rejected update wrote region")
			}
		})
	}
}
