package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestDiffComputeRegionRoundTrip(t *testing.T) {
	region := "eu-fsn"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/regional/diff" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req faas.DiffRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ComputeRegion == nil || *req.ComputeRegion != region {
			t.Errorf("placement request=%+v err=%v", req, err)
		}
		if req.BuildPlan == nil || req.BuildPlan.Class != "app" || req.Canary == nil || len(req.Canary.Stages) != 1 || req.Canary.Stages[0].Percent != 10 ||
			len(req.Workflows) != 1 || len(req.Workflows[0].Steps) != 1 || req.Workflows[0].Steps[0].WaitForDuration != "30s" {
			t.Errorf("preview intent was lost: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(faas.DiffResponse{Slug: "regional", Diff: faas.DiffPayload{Slug: "regional", ComputeRegion: region}})
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "fp_live_x")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Diff(context.Background(), "regional", faas.DiffRequest{
		ComputeRegion: &region, BuildPlan: &faas.BuildPlan{Class: "app", Framework: "go"},
		Canary:    &faas.CanaryPresetSpec{Preset: "custom", Stages: []faas.CustomStage{{Percent: 10, Duration: "30s"}}},
		Workflows: []faas.WorkflowSpec{{Name: "later", Steps: []faas.WorkflowStepSpec{{Name: "wait", WaitForDuration: "30s"}}}},
	})
	if err != nil || response.Diff.ComputeRegion != region || response.Blocking {
		t.Fatalf("preview response=%+v err=%v", response, err)
	}
}
