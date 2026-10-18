package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessFallbackAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.SeedAppForTimeline(t, e, "fallback-auth")
	admin := *e.Key()
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "notification reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	rule, err := (*e.Store()).CreateAlertRule(t.Context(), state.AlertRule{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "notification", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-fallback"
	version := int64(0)
	enabled := false
	body := api.SetMCPReadinessFallbackRequest{ExpectedVersion: &version, Enabled: &enabled}
	actionPath := path
	*e.Key() = reader
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 200 {
		t.Fatal("read scope", w.Code, w.Body.String())
	}
	if w := e.Do(t, "PUT", actionPath, body, nil); w.Code != 403 {
		t.Fatal("reader mutated", w.Code, w.Body.String())
	}
	if w := e.Do(t, "GET", actionPath, nil, nil); w.Code != 200 {
		t.Fatal("read history", w.Code, w.Body.String())
	}
	*e.Key() = admin
	if w := e.Do(t, "PUT", actionPath, body, nil); w.Code != 200 {
		t.Fatal("admin action", w.Code, w.Body.String())
	}
	if err = (*e.Store()).MarkMFAEnrolled(t.Context(), (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err = (*e.Store()).CreateSession(t.Context(), sid, (*e.Acct()).ID, "192.0.2.10", "notification-test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*(*e.S()).Sessions()).IssueWithSession(sid, (*e.Acct()).ID, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("PUT", actionPath, strings.NewReader(string(raw)))
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("pending MFA replayed action", w.Code, w.Body.String())
	}
}
