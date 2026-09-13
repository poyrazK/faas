package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	testStaleDeploymentID  = "11111111-1111-1111-1111-111111111111"
	testActiveDeploymentID = "22222222-2222-2222-2222-222222222222"
)

func TestDeploymentsRepairStaleDryRunDoesNotMutate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/ops/deployments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("status") != "pending,building,imaging,snapshotting" || r.URL.Query().Get("include_deleted") != "true" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		cutoff, err := time.Parse(time.RFC3339, r.URL.Query().Get("created_before"))
		if err != nil || time.Since(cutoff) < 119*time.Minute || time.Since(cutoff) > 121*time.Minute {
			t.Errorf("created_before = %q err=%v", r.URL.Query().Get("created_before"), err)
		}
		writeTestJSON(w, http.StatusOK, api.OperatorDeploymentListResponse{Deployments: []api.OperatorDeployment{{
			ID: testStaleDeploymentID, AppSlug: "deleted-app", AppStatus: "deleted", Status: "snapshotting",
		}}})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdDeploymentsDispatch([]string{"repair-stale", "--older-than", "2h"}); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "dry_run=true candidates=1") || !strings.Contains(out.String(), "app_status=deleted") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDeploymentsRepairStaleApplySkipsActiveBuild(t *testing.T) {
	var cancelCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/ops/deployments":
			writeTestJSON(w, http.StatusOK, api.OperatorDeploymentListResponse{Deployments: []api.OperatorDeployment{
				{ID: testStaleDeploymentID, AppSlug: "orphan", AppStatus: "active", Status: "building"},
				{ID: testActiveDeploymentID, AppSlug: "active", AppStatus: "active", Status: "building"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/ops/deployments/"+testStaleDeploymentID:
			writeTestJSON(w, http.StatusOK, api.OperatorDeploymentDetailResponse{Deployment: api.OperatorDeployment{ID: testStaleDeploymentID}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/deployments/"+testStaleDeploymentID+"/cancel":
			cancelCalls++
			if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "stale_deployment_reconciler" {
				t.Errorf("cancel query = %q", r.URL.RawQuery)
			}
			if r.Header.Get(operatorTraceIDHeader) == "" || r.Header.Get("Idempotency-Key") == "" {
				t.Error("repair mutation missing trace or idempotency header")
			}
			writeTestJSON(w, http.StatusOK, api.OperatorDeploymentMutationResponse{
				Deployment: api.OperatorDeployment{ID: testStaleDeploymentID, AppSlug: "orphan", Status: "cancelled"},
				Action:     "cancel", Reason: "stale_deployment_reconciler",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/ops/deployments/"+testActiveDeploymentID:
			writeTestJSON(w, http.StatusOK, api.OperatorDeploymentDetailResponse{
				Deployment: api.OperatorDeployment{ID: testActiveDeploymentID},
				Build:      &api.OperatorDeploymentBuild{ID: "build-active", Status: "running"},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdDeploymentsDispatch([]string{"repair-stale", "--yes"}); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if cancelCalls != 1 || !strings.Contains(out.String(), "reason=active_build") || !strings.Contains(out.String(), "repaired=1") {
		t.Fatalf("cancelCalls=%d stdout=%q", cancelCalls, out.String())
	}
}
