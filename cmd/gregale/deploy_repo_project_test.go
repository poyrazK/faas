package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale deploy --repo OWNER/NAME --ref REF --project` reviews the connected
// repository's project plan and applies exactly that reviewed source.
func TestDeployRepoProjectAppliesTheReviewedPlan(t *testing.T) {
	var scanned api.ProjectSourceRefScanRequest
	var applied api.ApplyCachedProjectPlanRequest
	applies := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/scan/source-ref":
			if err := json.NewDecoder(r.Body).Decode(&scanned); err != nil {
				t.Errorf("decode scan: %v", err)
			}
			main.WriteJSONTest(w, api.PlanResponse{CanApply: true, PlanToken: "reviewed-plan", ProjectSlug: "shop",
				Workloads: []api.PlanWorkload{{Name: "api", RootDir: "api"}, {Name: "web", RootDir: "web"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/apply-plan":
			applies++
			if err := json.NewDecoder(r.Body).Decode(&applied); err != nil {
				t.Errorf("decode apply: %v", err)
			}
			main.WriteJSONTest(w, api.ApplyResponse{ProjectID: "project-id", SubmissionAttemptID: r.Header.Get(api.DeploymentAttemptHeader)})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_repo_project")
	var output bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &output
	t.Cleanup(func() { *main.OsStdout = old })

	code := main.Run([]string{"deploy", "--repo", "octo/shop", "--ref", "main", "--project", "--only", "api", "--yes", "--no-wait"})
	if code != 0 {
		t.Fatalf("exit=%d output=%s", code, output.String())
	}
	if scanned.Repo != "octo/shop" || scanned.Ref != "main" || scanned.ProjectSlug != "shop" || strings.Join(scanned.Only, ",") != "api" {
		t.Fatalf("scan request = %+v", scanned)
	}
	if applies != 1 || applied.PlanToken != "reviewed-plan" || strings.Join(applied.Only, ",") != "api" {
		t.Fatalf("apply request = %+v (applies %d)", applied, applies)
	}
}

func TestDeployRepoProjectNeedsConfirmationAndRejectsSingleAppRollouts(t *testing.T) {
	applies := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan/source-ref":
			main.WriteJSONTest(w, api.PlanResponse{CanApply: true, PlanToken: "reviewed-plan", ProjectSlug: "shop"})
		case "/v1/projects/apply-plan":
			applies++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_repo_project")
	var output bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &output
	t.Cleanup(func() { *main.OsStdout = old })

	// Tests run without a TTY, so an unconfirmed apply must stop at the plan.
	if code := main.Run([]string{"deploy", "--repo", "octo/shop", "--ref", "main", "--project"}); code == 0 || applies != 0 {
		t.Fatalf("unconfirmed deploy: exit=%d applies=%d", code, applies)
	}
	if code := main.Run([]string{"deploy", "--repo", "octo/shop", "--ref", "main", "--project", "--traffic-percent", "10", "--yes"}); code == 0 || applies != 0 {
		t.Fatalf("single-app rollout flag accepted: exit=%d applies=%d", code, applies)
	}
}
