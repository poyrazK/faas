// adr: 623 — waiting reads exact receipts and reports commands without invoking probes.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingProjectPromotionWaitRejectsDifferentOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("wait mutated: %s", r.Method)
		}
		writeJSONTest(w, api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "another-operation", ProjectSlug: "shop", ToEnvironment: "production", Status: "succeeded"})
	}))
	defer srv.Close()
	_, _, err := waitForProjectEnvironmentPromotion(context.Background(), NewClient(srv.URL, "token"), "shop", "production", "selected", time.Second, api.ProjectEnvironmentPromotionStatusResponse{BindingsRequired: true}, nil)
	if err == nil {
		t.Fatal("wait accepted an unrelated success receipt")
	}
}
func TestBindingProjectPromotionReceiptAndExactCommands(t *testing.T) {
	caller, target := uuid.NewString(), uuid.NewString()
	report := api.ProjectReleaseCheckResponse{ProjectID: uuid.NewString(), Environment: "production", TTLSeconds: 1800, ExpectedActiveReleaseID: uuid.NewString(), Passed: true, CheckedAt: time.Now().UTC(), Members: []api.ProjectReleaseSetMemberResponse{{AppID: uuid.NewString(), DeploymentID: caller}, {AppID: uuid.NewString(), DeploymentID: target}}, Checks: []api.BindingCheckReport{{App: "api", Scope: "production", DeploymentID: caller, Passed: true, Bindings: []api.BindingCheckBindingResult{{Type: api.BindingTypeService, Name: "billing", Status: "blocked"}}}, {App: "billing", Scope: "production", DeploymentID: target, Passed: true}}}
	report.GraphDigest = api.ProjectReleaseGraphDigest(report)
	status := api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "operation", ProjectSlug: "shop", ToEnvironment: "production", Status: "succeeded", BindingsRequired: true, BindingsCheck: &report, ReleaseGraph: &api.ProjectEnvironmentPromotionReleaseGraphResponse{TargetReleaseSetID: uuid.NewString(), PreviousTargetReleaseSetID: report.ExpectedActiveReleaseID, TTLSeconds: 1800}, Workloads: []api.ProjectEnvironmentPromotionStatusWorkloadResponse{{WorkloadSlug: "api", TargetDeploymentID: caller}, {WorkloadSlug: "billing", TargetDeploymentID: target}}}
	if err := validateBindingProjectPromotionReceipt(status, status, "shop", "production", "operation"); err != nil {
		t.Fatal(err)
	}
	status.Workloads[0].TargetDeploymentID = uuid.NewString()
	if err := validateBindingProjectPromotionReceipt(status, status, "shop", "production", "operation"); err == nil {
		t.Fatal("accepted mismatched activated membership")
	}
	status.Workloads[0].TargetDeploymentID = caller
	report.Passed = false
	commands := promotionVerificationCommands(status)
	if len(commands) != 1 || !strings.Contains(commands[0], "--deployment "+caller) || !strings.Contains(commands[0], "--target-deployment "+target) {
		t.Fatalf("wrong exact verification command: %v", commands)
	}
}

func TestBindingProjectPromotionCLIAdmitsOnceAndWaitsWithGETs(t *testing.T) {
	resetJSONOut(t)
	oldOut, oldErr := osStdout, osStderr
	var out, errOut bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &errOut, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, false })
	oldInterval := projectEnvironmentPromotionPollInterval
	projectEnvironmentPromotionPollInterval = time.Millisecond
	t.Cleanup(func() { projectEnvironmentPromotionPollInterval = oldInterval })
	posts, gets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/promotion-preview"):
			writeJSONTest(w, api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", CanPromote: true, PromotionHash: "hash", PromotionToken: "token", ReleaseGraphMode: true})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/promote-with-bindings"):
			posts++
			var req api.PromoteProjectEnvironmentRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || !req.RequireBindings {
				t.Error("CLI omitted binding contract")
			}
			w.WriteHeader(202)
			writeJSONTest(w, api.ProjectEnvironmentPromotionResponse{PromotionID: "operation", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: "hash", Status: "running", BindingsRequired: true})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/promotions/operation"):
			gets++
			writeJSONTest(w, api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "operation", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: "hash", Status: "failed", BindingsRequired: true, Error: "snapshot changed"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "token")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if code := cmdProjectsEnvironmentPromote([]string{"shop", "--from", "staging", "--to", "production", "--require-bindings", "--yes", "--wait", "--progress", "--timeout", "1"}); code != 1 {
		t.Fatalf("expected failed receipt: %d %s %s", code, out.String(), errOut.String())
	}
	if posts != 1 || gets != 1 {
		t.Fatalf("wait drove execution: POSTs=%d GETs=%d", posts, gets)
	}
}
