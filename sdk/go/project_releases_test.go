package faas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProjectReleaseCheckedRequestAndBlockedReceipt(t *testing.T) {
	empty := ""
	report := ProjectReleaseCheckResponse{ProjectID: "142b7504-f03a-4ee2-aeb3-14d922a845d4", Environment: "production", ExpectedActiveReleaseID: empty, TTLSeconds: 1800, CheckedAt: time.Now().UTC(), GraphDigest: "digest", Passed: false, Members: []ProjectReleaseSetMemberResponse{}, Checks: []BindingCheckReport{}, Blockers: []BindingCheckFinding{{Code: "verification_missing", Message: "Run the exact probe."}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request PublishProjectReleaseSetRequest
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedActiveReleaseID == nil || *request.ExpectedActiveReleaseID != empty || request.Deployments["api"] != "a2b9cc53-907f-4b5c-88a4-fd0c21214556" {
			t.Error("request lost exact selection or explicit empty predecessor")
		}
		if r.URL.Path == "/v1/projects/shop/environments/production/release-sets/check" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(report)
			return
		}
		if r.URL.Path != "/v1/projects/shop/environments/production/release-sets" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(Problem{Status: 409, Code: "project_release_check_failed", ProjectReleaseCheck: &report})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	req := PublishProjectReleaseSetRequest{TTLSeconds: 1800, ExpectedActiveReleaseID: &empty, Deployments: map[string]string{"api": "a2b9cc53-907f-4b5c-88a4-fd0c21214556"}}
	checked, err := client.CheckProjectReleaseSet(context.Background(), "shop", "production", req)
	if err != nil || checked.Passed || len(checked.Blockers) != 1 {
		t.Fatalf("check: %+v %v", checked, err)
	}
	_, err = client.PublishProjectReleaseSet(context.Background(), "shop", "production", req)
	var problem *APIError
	if !errors.As(err, &problem) || problem.Problem.ProjectReleaseCheck == nil || len(problem.Problem.ProjectReleaseCheck.Blockers) != 1 {
		t.Fatalf("blocked receipt lost evidence: %v", err)
	}
}
