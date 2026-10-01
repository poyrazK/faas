package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestComputeRegionsAndAppCreation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	region := "eu-fsn"
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
