package mcpreadiness

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessFallbackHTTP(t *testing.T) {
	s := state.NewMemStore()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, "fallback-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "fallback-http", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: a.ID, AppID: app.ID, Name: "fallback", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://primary.example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	checks, seals, audits := 0, 0, 0
	h := Handler{Store: s, FallbackURL: func(_ context.Context, url string) *api.Problem {
		checks++
		if strings.Contains(url, "127.0.0.1") {
			return api.ErrValidation("destination denied")
		}
		return nil
	}, FallbackSeal: func(_ context.Context, secret string) ([]byte, *api.Problem) {
		seals++
		if secret != "PRIVATE_SIGNING_KEY" {
			t.Fatal("incorrect seal input")
		}
		return []byte("SEALED_VALUE"), nil
	}, Audit: func(_ context.Context, kind string, _ *string, fields map[string]any) {
		audits++
		b, _ := json.Marshal(fields)
		if strings.Contains(string(b), "PRIVATE_SIGNING_KEY") || strings.Contains(string(b), "SEALED_VALUE") || kind != "mcp.readiness_fallback.updated" {
			t.Fatal("audit leaked secret", string(b))
		}
	}}
	call := func(method, id, query, body string, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/fallback"+query, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		h.Fallback(w, r, account)
		return w
	}
	body := `{"expected_version":0,"enabled":true,"webhook_url":"https://fallback.example.com/hook","webhook_secret":"PRIVATE_SIGNING_KEY","after_failures":2}`
	if w := call("GET", rule.ID, "", "", a); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":0`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, b := range []string{strings.Replace(body, `"expected_version":0,`, "", 1), strings.Replace(body, `"enabled":true,`, "", 1), strings.Replace(body, `"after_failures":2`, `"after_failures":11`, 1), body + body, strings.TrimSuffix(body, "}") + `,"unknown":1}`} {
		if w := call("PUT", rule.ID, "", b, a); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if checks != 0 || seals != 0 {
		t.Fatal("invalid request reached destination validation")
	}
	if w := call("PUT", rule.ID, "", strings.Replace(body, "https://fallback.example.com", "http://127.0.0.1", 1), a); w.Code != 400 || seals != 0 {
		t.Fatal("egress bypass", w.Code)
	}
	if w := call("PUT", rule.ID, "", body, state.Account{ID: uuid.NewString()}); w.Code != 404 {
		t.Fatal("foreign app", w.Code)
	}
	if w := call("GET", uuid.NewString(), "", "", a); w.Code != 404 {
		t.Fatal("foreign rule", w.Code)
	}
	if w := call("PUT", rule.ID, "?x=1", body, a); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("PUT", rule.ID, "", body, a); w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_SIGNING_KEY") || strings.Contains(w.Body.String(), "SEALED_VALUE") || !strings.Contains(w.Body.String(), "***") {
		t.Fatal("secrets exposed", w.Code, w.Body.String())
	}
	if w := call("PUT", rule.ID, "", body, a); w.Code != 409 {
		t.Fatal("version fence", w.Code)
	}
	if w := call("PUT", rule.ID, "", `{"expected_version":1,"enabled":false}`, a); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if audits != 2 {
		t.Fatal("missing audit", audits)
	}
}
