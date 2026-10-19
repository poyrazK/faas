// adr: 20261017210000
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegressionReviewSelectionAndStableWriteKey(t *testing.T) {
	route := "GET /a + b"
	version := "11111111-1111-4111-8111-111111111111"
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/v1/apps/alpha/debug/regressions/review" || r.URL.Query().Get("route") != route || r.URL.Query().Get("deployment_id") != "dep" {
				t.Error("exact selectors not preserved")
			}
			_ = json.NewEncoder(w).Encode(DebugRegressionReview{Version: version})
			return
		}
		var req GuardedDebugRegressionActionRequest
		if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "stable-review-key" || json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedVersion != version || req.ExpectedAppID != "app" || req.Reason != "Investigate" {
			t.Error("guard or retry key changed")
		}
		_ = json.NewEncoder(w).Encode(DebugRegressionActionResponse{Review: &DebugRegressionReview{Version: version}})
	}))
	defer b.Close()
	c := NewClient(b.URL, "fixture")
	v, err := c.GetAppDebugRegressionReview(t.Context(), "alpha", "dep", route)
	if err != nil || v.Version != version {
		t.Fatal(v, err)
	}
	_, err = c.ApplyAppDebugRegressionAction(ContextWithIdempotencyKey(t.Context(), "stable-review-key"), "alpha", GuardedDebugRegressionActionRequest{DeploymentID: "dep", Route: route, Action: "resolve", ExpectedAppID: "app", ExpectedVersion: version, Reason: "Investigate"})
	if err != nil {
		t.Fatal(err)
	}
}
