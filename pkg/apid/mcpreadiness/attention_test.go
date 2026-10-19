package mcpreadiness

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessAttentionHTTP(t *testing.T) {
	s := state.NewMemStore()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, "attention-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "attention-http", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "attention", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	coverage := strings.Repeat("a", 64)
	payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}})
	event, err := s.RecordMCPReadinessObservation(ctx, state.MCPReadinessObservation{Rule: rule, At: time.Now().Add(-time.Minute), Breached: true, Coverage: coverage, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	audits := 0
	h := Handler{Store: s, Audit: func(_ context.Context, kind string, _ *string, fields map[string]any) {
		if kind != "mcp.readiness_attention.acknowledge" || fields["incident_id"] != event.IncidentID {
			t.Fatal(kind, fields)
		}
		audits++
	}}
	call := func(method, id, q, body string, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/attention"+q, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		h.Attention(w, r, account)
		return w
	}
	for _, q := range []string{"?scope=", "?scope=staging&scope=production", "?other=x", "?include_resolved=bad", "?limit=0", "?after=bad", "?scope=%zz"} {
		if w := call("GET", "", q, "", a); w.Code != 400 {
			t.Fatal(q, w.Code, w.Body.String())
		}
	}
	if w := call("GET", "", "?scope=staging", "", a); w.Code != 200 || !strings.Contains(w.Body.String(), event.IncidentID) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	body := `{"request_id":"` + uuid.NewString() + `","expected_version":0,"action":"acknowledge","reason":"Investigating"}`
	for _, b := range []string{strings.Replace(body, `"expected_version":0,`, "", 1), strings.TrimSuffix(body, "}") + `,"unknown":true}`, body + body, strings.Replace(body, `"Investigating"`, `" "`, 1)} {
		if w := call("POST", event.IncidentID, "", b, a); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("POST", event.IncidentID, "?scope=staging", body, a); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("POST", event.IncidentID, "", body, state.Account{ID: "foreign"}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	first := call("POST", event.IncidentID, "", body, a)
	if first.Code != 200 || audits != 1 {
		t.Fatal(first.Code, first.Body.String(), audits)
	}
	if w := call("POST", event.IncidentID, "", body, a); w.Code != 200 || w.Body.String() != first.Body.String() {
		t.Fatal("replay", w.Code, w.Body.String())
	}
	if w := call("POST", event.IncidentID, "", strings.Replace(body, "Investigating", "Changed", 1), a); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("GET", event.IncidentID, "?limit=1", "", a); w.Code != 200 || !strings.Contains(w.Body.String(), "Investigating") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, q := range []string{"?scope=staging", "?before=bad", "?after=" + event.IncidentID, "?limit=51"} {
		if w := call("GET", event.IncidentID, q, "", a); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w := call("GET", uuid.NewString(), "", "", a); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "", "", "", state.Account{ID: "foreign"}); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
