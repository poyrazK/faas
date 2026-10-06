package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteClientErrorSDKConfigurationAndEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/demo/route-health/gate" {
			var request faas.SetRouteHealthGateRequest
			if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Routes) != 1 || !slices.Equal(request.Routes[0].WatchStatuses, []int{403, 422}) {
				t.Error("watched status intent binding")
			}
			_, _ = w.Write([]byte(`{"app_id":"app","mode":"report","revision":1,"routes":[{"method":"POST","path":"/checkout","watch_statuses":[403,422]}]}`))
			return
		}
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/candidate" {
			t.Error("deployment binding")
		}
		_, _ = w.Write([]byte(`{"status":"healthy","client_error_status":"regressed","client_error_reason":"consecutive_watched_status_regression","routes":[{"method":"POST","path":"/checkout","status":"healthy","watch_statuses":[403,422],"client_errors":{"status":"regressed","reason":"consecutive_watched_status_regression","minimum_requests":20,"minimum_responses":2,"rate_floor":0.05,"rate_delta":0.05,"rate_factor":3,"statuses":[{"status_code":403,"status":"regressed","reason":"consecutive_watched_status_regression","windows":[{"start":"2026-10-03T00:00:00Z","end":"2026-10-03T00:01:00Z","candidate":{"requests":100,"responses":20,"rate":0.2},"stable":{"requests":100,"responses":0,"rate":0},"status":"regressed","reason":"watched_status_rate_increased"}]}]}}]}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	gate, err := client.SetRouteHealthGate(context.Background(), "demo", faas.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []faas.RouteHealthRoute{{Method: "POST", Path: "/checkout", WatchStatuses: []int{403, 422}}}})
	if err != nil || !slices.Equal(gate.Routes[0].WatchStatuses, []int{403, 422}) {
		t.Fatal("configuration was lost")
	}
	report, err := client.GetRouteHealthReport(context.Background(), "demo", "candidate")
	if err != nil || report.Status != "healthy" || report.ClientErrorStatus != "regressed" {
		t.Fatal("advisory result was lost")
	}
	c := report.Routes[0].ClientErrors
	if c == nil || c.MinimumResponses != 2 || c.Statuses[0].StatusCode != 403 || c.Statuses[0].Windows[0].Candidate.Responses != 20 || c.Statuses[0].Windows[0].Stable.Rate != 0 {
		t.Fatal("weighted evidence was lost")
	}
}
