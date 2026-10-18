package apidserver_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyFallbackHTTP(t *testing.T) {
	previous := *apidserver.HostIdentitiesForUnseal
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return (*apidserver.MfaIdentities)() }
	t.Cleanup(func() { *apidserver.HostIdentitiesForUnseal = previous })
	e := apidserver.Setup(t, api.PlanPro)
	t.Cleanup(apidserver.WithTestRecipient(t))
	store := *e.Store()
	app := apidserver.SeedAppForTimeline(t, e, "native-fallback")
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "fallback", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	on, off := true, false
	path := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-fallback"
	request := api.SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &on, Provider: "pagerduty", Region: "eu", IntegrationKey: strings.Repeat("a", 32), AfterFailures: 1}
	admin := *e.Key()
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(t.Context(), rule.AccountID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = reader
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 403 {
		t.Fatal("read-only write", w.Code)
	}
	*e.Key() = admin
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"provider":"pagerduty"`) || !strings.Contains(w.Body.String(), `"region":"eu"`) || strings.Contains(w.Body.String(), request.IntegrationKey) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 409 {
		t.Fatal("stale version accepted", w.Code)
	}
	mixed := request
	mixed.WebhookSecret = "wrong-provider"
	mixed.ExpectedVersion = &one
	if w := e.Do(t, "PUT", path, mixed, nil); w.Code != 400 {
		t.Fatal("mixed provider fields", w.Code)
	}
	incomingPath := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-pagerduty-acknowledgements"
	secret := strings.Repeat("s", 32)
	binding := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &zero, Enabled: &on, Destination: "fallback", ExpectedFallbackVersion: &one, ServiceID: "PSVC001", SigningSecret: secret}
	if w := e.Do(t, "PUT", incomingPath, binding, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"fallback_version":1`) || !strings.Contains(w.Body.String(), `"destination":"fallback"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"default"}})
	first, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: at, Breached: true, Coverage: strings.Repeat("a", 64), Payload: payload, ObservedValue: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"event": map[string]any{"id": "fallback-event", "event_type": "incident.acknowledged", "resource_type": "incident", "occurred_at": time.Now().UTC(), "data": map[string]any{"id": "PINC001", "type": "incident", "status": "acknowledged", "incident_key": "gregale:mcp:" + first.IncidentID, "self": "https://api.eu.pagerduty.com/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}}})
	post := func() int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/mcp-readiness/pagerduty/"+rule.ID, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		r.Header.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w.Code
	}
	if code := post(); code != 202 {
		t.Fatal(code)
	}
	receipts, err := store.ListMCPReadinessPagerDutyAcknowledgementEvents(t.Context(), rule.AccountID, app.ID, rule.ID, "", 20)
	if err != nil || len(receipts.Events) != 0 {
		t.Fatal("unactivated fallback acknowledged", receipts, err)
	}
	c, won, err := store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won || c.Provider != "webhook" {
		t.Fatal(c, won, err)
	}
	if err = store.UpdateAlertDeliveryStatus(t.Context(), c.Delivery.ID, state.AlertDeliveryFailed, 1, 503, "", &at); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteMCPReadinessDelivery(t.Context(), c.EventID, c.Token, at); err != nil {
		t.Fatal(err)
	}
	c, won, err = store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won || c.Provider != "pagerduty" || c.Rule.WebhookURL != api.MCPReadinessPagerDutyEUEndpoint {
		t.Fatal(c.Provider, won, err)
	}
	if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), c.EventID, c.Token, at, state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code := post(); code != 202 {
			t.Fatal(code)
		}
	}
	*e.Key() = reader
	if w := e.Do(t, "GET", incomingPath+"/events", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"destination":"fallback"`) || strings.Contains(w.Body.String(), secret) {
		t.Fatal(w.Code, w.Body.String())
	}
	receipts, err = store.ListMCPReadinessPagerDutyAcknowledgementEvents(t.Context(), rule.AccountID, app.ID, rule.ID, "", 20)
	if err != nil || len(receipts.Events) != 1 || receipts.Events[0].Outcome != "acknowledged" {
		t.Fatal(receipts, err)
	}
	*e.Key() = admin
	primaryPath := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-pagerduty"
	if w := e.Do(t, "PUT", primaryPath, api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &on, Region: "eu", IntegrationKey: request.IntegrationKey}, nil); w.Code != 400 {
		t.Fatal("identical primary accepted", w.Code)
	}
	if w := e.Do(t, "PUT", path, api.SetMCPReadinessFallbackRequest{ExpectedVersion: &one, Enabled: &off}, nil); w.Code != 200 {
		t.Fatal("disable", w.Code, w.Body.String())
	}
	if code := post(); code != 202 {
		t.Fatal("outgoing disable revoked old binding", code)
	}
}
