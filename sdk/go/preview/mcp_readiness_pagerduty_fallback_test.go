package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPReadinessPagerDutyFallbackContracts(t *testing.T) {
	zero, one := int64(0), int64(1)
	on, off := true, false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		var data map[string]any
		if r.Method == "PUT" {
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Error(err)
			}
		}
		if strings.HasSuffix(r.URL.Path, "/mcp-readiness-fallback") {
			if r.Method == "PUT" && data["enabled"] == true {
				if data["provider"] != "pagerduty" || data["region"] != "eu" || data["integration_key"] != strings.Repeat("a", 32) || data["after_failures"] != float64(2) {
					t.Error("fallback key or routing fields missing")
				}
				if _, ok := data["webhook_secret"]; ok {
					t.Error("webhook credential mixed with PagerDuty")
				}
			} else if r.Method == "PUT" && len(data) != 2 {
				t.Error("disable retains provider fields")
			}
			_, _ = w.Write([]byte(`{"version":1,"enabled":true,"provider":"pagerduty","region":"eu","integration_key_masked":"***","after_failures":2}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/mcp-readiness-pagerduty-acknowledgements") {
			t.Error(r.URL)
		}
		if data["destination"] != "fallback" || data["expected_fallback_version"] != float64(1) {
			t.Error("fallback acknowledgement binding omitted")
		}
		if _, ok := data["expected_pagerduty_version"]; ok {
			t.Error("primary version leaked into fallback binding")
		}
		_, _ = w.Write([]byte(`{"version":1,"enabled":true,"destination":"fallback","fallback_version":1,"region":"eu","service_id":"PSVC001","webhook_path":"/v1/webhooks/mcp-readiness/pagerduty/rule"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	policy, err := client.SetMCPReadinessFallback(ctx, "demo", "rule", SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &on, Provider: "pagerduty", Region: "eu", IntegrationKey: strings.Repeat("a", 32), AfterFailures: 2})
	if err != nil || policy.Provider != "pagerduty" || policy.Region != "eu" || policy.IntegrationKeyMasked != "***" {
		t.Fatal(policy, err)
	}
	if _, err = client.SetMCPReadinessFallback(ctx, "demo", "rule", SetMCPReadinessFallbackRequest{ExpectedVersion: &one, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	bound, err := client.SetMCPReadinessPagerDutyAcknowledgements(ctx, "demo", "rule", SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &zero, Enabled: &on, Destination: "fallback", ExpectedFallbackVersion: &one, ServiceID: "PSVC001", SigningSecret: strings.Repeat("s", 32)})
	if err != nil || bound.Destination != "fallback" || bound.FallbackVersion != 1 || bound.PagerDutyVersion != 0 {
		t.Fatal(bound, err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
