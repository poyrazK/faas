package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021143000 — signup-set resolves plan names and sends the
// settings, signups lists sign-ups, and a missing display name never
// reaches the API.
func TestCmdConsumerSignup(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1") // adr: 20261018090210 — exercises released monetization
	var put api.PutConsumerSignupRequest
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/my-api/consumer-plans":
			_ = json.NewEncoder(w).Encode(api.APIConsumerPlanListResponse{Plans: []api.APIConsumerPlanResponse{{ID: "p-pro", Name: "pro"}}})
		case "PUT /v1/apps/my-api/consumer-signup":
			_ = json.NewDecoder(r.Body).Decode(&put)
			_ = json.NewEncoder(w).Encode(api.ConsumerSignupSettingsResponse{App: "my-api", Enabled: *put.Enabled, Provider: "stripe",
				PlanIDs: put.PlanIDs, KeyScopes: []string{"write", "billing"}, DisplayName: put.DisplayName, TrialDays: put.TrialDays,
				DailyLimit: 100, PageURL: "https://gregale.dev/v1/consumer-signup/my-api/page"})
		case "GET /v1/apps/my-api/consumer-signups":
			_ = json.NewEncoder(w).Encode(api.ConsumerSignupListResponse{Signups: []api.ConsumerSignupResponse{{ID: "s1", Email: "ada@example.com",
				Name: "Ada", Status: "completed", ConsumerID: "c1", CreatedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}}})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := main.CmdConsumers([]string{"signup-set", "my-api", "--display-name", "Weather API", "--plans", "pro", "--trial-days", "14"}); code != 0 {
		t.Fatalf("signup-set exit %d", code)
	}
	if put.DisplayName != "Weather API" || len(put.PlanIDs) != 1 || put.PlanIDs[0] != "p-pro" || put.TrialDays != 14 || put.Enabled == nil || !*put.Enabled ||
		!strings.Contains(stdout.String(), "/v1/consumer-signup/my-api/page") {
		t.Fatalf("put = %+v, out = %s", put, stdout)
	}
	if code := main.CmdConsumers([]string{"signups", "my-api"}); code != 0 || !strings.Contains(stdout.String(), "ada@example.com") {
		t.Fatalf("signups exit %d, out = %s", code, stdout)
	}
	if code := main.CmdConsumers([]string{"signup-set", "my-api", "--plans", "pro"}); code == 0 {
		t.Fatal("signup-set without a display name succeeded")
	}
}
