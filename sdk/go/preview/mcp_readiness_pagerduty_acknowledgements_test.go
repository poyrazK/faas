package preview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPReadinessPagerDutyAcknowledgements(t *testing.T) {
	zero, one := int64(0), int64(1)
	on, off := true, false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/events") {
			if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("before") != "cursor" {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"events":[{"region":"eu","service_id":"PSVC001","id":"00000000-0000-0000-0000-000000000001","event_id":"provider-1","incident_id":"00000000-0000-0000-0000-000000000001","provider_incident_id":"PINC001","occurred_at":"2026-10-17T00:00:00Z","received_at":"2026-10-17T00:00:01Z","policy_version":1,"attention_version":1,"action":"release","destination":"fallback","outcome":"released"}]}`))
			return
		}
		if r.URL.Path != "/v1/apps/demo/alerts/rule/mcp-readiness-pagerduty-acknowledgements" {
			t.Error(r.URL)
		}
		if calls == 5 && r.URL.Query().Get("destination") != "fallback" {
			t.Error(r.URL)
		}
		if r.Method == "PUT" {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req["enabled"] == true {
				if req["sync_unacknowledgements"] != true || req["service_id"] != "PSVC001" || req["signing_secret"] != strings.Repeat("s", 32) || req["expected_pagerduty_version"] != float64(1) {
					t.Error("binding missing")
				}
			} else if len(req) != 2 {
				t.Error("disabled request retains binding")
			}
		}
		_, _ = w.Write([]byte(`{"version":1,"enabled":true,"sync_unacknowledgements":true,"region":"eu","pagerduty_version":1,"service_id":"PSVC001","signing_secret_masked":"***","webhook_path":"/v1/webhooks/mcp-readiness/pagerduty/rule"}`))
	}))
	defer server.Close()
	c, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.GetMCPReadinessPagerDutyAcknowledgements(context.Background(), "demo", "rule")
	if err != nil || !p.SyncUnacknowledgements || p.Region != "eu" || p.WebhookPath == "" {
		t.Fatal(p, err)
	}
	for _, r := range []SetMCPReadinessPagerDutyAcknowledgementsRequest{{ExpectedVersion: &zero, Enabled: &on, SyncUnacknowledgements: true, ExpectedPagerDutyVersion: &one, ServiceID: "PSVC001", SigningSecret: strings.Repeat("s", 32)}, {ExpectedVersion: &one, Enabled: &off}} {
		if _, err = c.SetMCPReadinessPagerDutyAcknowledgements(context.Background(), "demo", "rule", r); err != nil {
			t.Fatal(err)
		}
	}
	events, err := c.ListMCPReadinessPagerDutyAcknowledgementEvents(context.Background(), "demo", "rule", "cursor", 1)
	if err != nil || len(events.Events) != 1 || events.Events[0].Outcome != "released" || events.Events[0].Action != "release" || events.Events[0].Destination != "fallback" || events.Events[0].Region != "eu" || events.Events[0].ServiceID != "PSVC001" {
		t.Fatal(events, err)
	}

	if _, err = c.GetMCPReadinessPagerDutyAcknowledgements(context.Background(), "demo", "rule", "fallback"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetMCPReadinessPagerDutyAcknowledgements(context.Background(), "demo", "rule", "other"); err == nil {
		t.Fatal("invalid selector accepted")
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}

func TestMCPReadinessPagerDutyHoldContract(t *testing.T) {
	var attention MCPReadinessIncidentAttention
	raw := []byte(`{"provider_acknowledgements":[{"destination":"primary","provider_incident_id":"PINC001","provider_event_id":"event","service_id":"PSVC001","region":"eu","occurred_at":"2026-10-17T00:00:00Z","received_at":"2026-10-17T00:00:01Z","policy_version":1,"release_sync_enabled":true}]}`)
	if err := json.Unmarshal(raw, &attention); err != nil || len(attention.ProviderAcknowledgements) != 1 || !attention.ProviderAcknowledgements[0].ReleaseSyncEnabled || attention.ProviderAcknowledgements[0].Destination != "primary" {
		t.Fatal(attention, err)
	}
}
