package preview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPReadinessReminderDefaults(t *testing.T) {
	zero := int64(0)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/demo/alerts/rule/mcp-readiness-reminder-defaults":
			if r.Method == "PUT" {
				var body SetMCPReadinessReminderDefaultsRequest
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body.ExpectedVersion == nil || *body.ExpectedVersion != 0 || body.ReminderPolicy == nil || body.ReminderPolicy.MaxReminders != 0 {
					t.Error(body, e)
				}
			}
			_ = json.NewEncoder(w).Encode(MCPReadinessReminderDefaults{Version: 0})
		case "/v1/apps/demo/mcp-readiness-attention":
			_ = json.NewEncoder(w).Encode(MCPReadinessAttentionPage{Incidents: []MCPReadinessIncidentAttention{{ReminderPolicySource: "rule_default", ReminderDefaultsVersion: &zero}}})
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, e := NewClient(server.URL, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if v, e := c.GetMCPReadinessReminderDefaults(context.Background(), "demo", "rule"); e != nil || v.Version != 0 {
		t.Fatal(v, e)
	}
	if _, e = c.SetMCPReadinessReminderDefaults(context.Background(), "demo", "rule", SetMCPReadinessReminderDefaultsRequest{ExpectedVersion: &zero, ReminderPolicy: &MCPReadinessReminderPolicy{}}); e != nil {
		t.Fatal(e)
	}
	v, e := c.ListMCPReadinessAttention(context.Background(), "demo", "staging", "", 20, false)
	if e != nil || len(v.Incidents) != 1 || v.Incidents[0].ReminderDefaultsVersion == nil || *v.Incidents[0].ReminderDefaultsVersion != 0 || v.Incidents[0].ReminderPolicySource != "rule_default" {
		t.Fatal(v, e)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
