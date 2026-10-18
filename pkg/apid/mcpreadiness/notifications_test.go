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

func TestMCPReadinessNotificationsHTTP(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	a, err := s.CreateAccount(ctx, "notifications-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "notifications-http", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "rule", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
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
		if kind != "mcp.readiness_notification.abandon" || fields["event_id"] != event.EventID {
			t.Fatal(kind, fields)
		}
		audits++
	}}
	call := func(method, q, body string, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/apps/notifications-http/mcp-readiness-notifications"+q, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		h.Notifications(w, r, account)
		return w
	}
	for _, q := range []string{"?scope=", "?scope=staging&scope=production", "?unknown=x", "?limit=0", "?after=bad", "?scope=%zz"} {
		if w := call("GET", q, "", a); w.Code != 400 {
			t.Fatal(q, w.Code, w.Body.String())
		}
	}
	if w := call("GET", "?scope=staging", "", a); w.Code != 200 || !strings.Contains(w.Body.String(), event.EventID) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	body := `{"request_id":"` + uuid.NewString() + `","event_id":"` + event.EventID + `","expected_version":0,"action":"abandon","reason":"Receiver retired"}`
	for _, invalid := range []string{strings.Replace(body, `"expected_version":0,`, "", 1), strings.Replace(body, `"reason":"Receiver retired"`, `"reason":" "`, 1), strings.TrimSuffix(body, "}") + `,"unknown":true}`, body + body} {
		if w := call("POST", "", invalid, a); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("POST", "?scope=staging", body, a); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "", body, state.Account{ID: "foreign"}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	first := call("POST", "", body, a)
	if first.Code != 200 || audits != 1 {
		t.Fatal(first.Code, first.Body.String(), audits)
	}
	replay := call("POST", "", body, a)
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatal("receipt replay", replay.Code, replay.Body.String())
	}
	if w := call("POST", "", strings.Replace(body, "Receiver retired", "Changed reason", 1), a); w.Code != 409 {
		t.Fatal("changed idempotent request", w.Code, w.Body.String())
	}
	if w := call("GET", "", "", state.Account{ID: "foreign"}); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
