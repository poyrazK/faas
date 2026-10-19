package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261019120041
func TestProjectDeploymentCheckClientRejectsContradictoryReports(t *testing.T) {
	for _, contradictory := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "contradictory"}[contradictory], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/projects/shop/deployment-readiness" {
					t.Error("unexpected endpoint", r.Method, r.URL)
				}
				checks := []ProjectDeploymentCheck{{Code: "account_usage", Status: "unknown", Message: "usage unavailable"}}
				report := ProjectDeploymentReadiness{SchemaVersion: 1, ProjectSlug: "shop", Environment: "production", CheckedAt: time.Now(), Status: "unknown", Checks: checks}
				if contradictory {
					report.Status = "ready"
				}
				_ = json.NewEncoder(w).Encode(report)
			}))
			defer server.Close()
			result, err := NewClient(server.URL, "fixture").CheckProjectDeployment(t.Context(), "shop", ProjectDeploymentCheckRequest{})
			if contradictory && err == nil {
				t.Fatal("inconsistent ready report accepted")
			}
			if !contradictory && (err != nil || result.Status != "unknown") {
				t.Fatal("valid unknown report rejected", result, err)
			}
		})
	}
}
