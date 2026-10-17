package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessNotificationsAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.SeedAppForTimeline(t, e, "notifications-auth")
	path := "/v1/apps/" + app.Slug + "/mcp-readiness-notifications"
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
	coverage := strings.Repeat("a", 64)
	p, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": coverage, "mcp_readiness_scopes": []string{"staging"}})
	event, err := (*e.Store()).RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: time.Now().Add(-time.Minute), Breached: true, Coverage: coverage, Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	version := int64(0)
	body := api.MCPReadinessNotificationAction{RequestID: uuid.NewString(), EventID: event.EventID, ExpectedVersion: &version, Action: "abandon", Reason: "Receiver retired"}
	*e.Key() = reader
	if w := e.Do(t, "GET", path+"?scope=staging", nil, nil); w.Code != 200 {
		t.Fatal("read scope", w.Code, w.Body.String())
	}
	if w := e.Do(t, "POST", path+"/actions", body, nil); w.Code != 403 {
		t.Fatal("reader mutated", w.Code, w.Body.String())
	}
	*e.Key() = admin
	if w := e.Do(t, "POST", path+"/actions", body, nil); w.Code != 200 {
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
	r := httptest.NewRequest("POST", path+"/actions", strings.NewReader(string(raw)))
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("pending MFA replayed action", w.Code, w.Body.String())
	}
}
