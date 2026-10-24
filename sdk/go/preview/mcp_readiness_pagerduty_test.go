package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPReadinessPagerDuty(t *testing.T) {
	zero := int64(0)
	on, off := true, false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/demo/alerts/rule/mcp-readiness-pagerduty" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PUT" {
			var body SetMCPReadinessPagerDutyRequest
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body.ExpectedVersion == nil || *body.ExpectedVersion != 0 || body.Enabled == nil {
				t.Error(body.ExpectedVersion, e)
			} else if *body.Enabled && (body.IntegrationKey != strings.Repeat("a", 32) || body.Region != "eu") || !*body.Enabled && body.IntegrationKey != "" {
				t.Error("lost write-only fields")
			}
		}
		_ = json.NewEncoder(w).Encode(MCPReadinessPagerDutyPolicy{Version: 1, Enabled: true, Region: "eu", IntegrationKeyMasked: "***"})
	}))
	defer server.Close()
	c, e := NewClient(server.URL, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if v, e := c.GetMCPReadinessPagerDuty(context.Background(), "demo", "rule"); e != nil || v.IntegrationKeyMasked != "***" || v.Region != "eu" {
		t.Fatal(v, e)
	}
	for _, req := range []SetMCPReadinessPagerDutyRequest{{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32), Region: "eu"}, {ExpectedVersion: &zero, Enabled: &off}} {
		if _, e = c.SetMCPReadinessPagerDuty(context.Background(), "demo", "rule", req); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
