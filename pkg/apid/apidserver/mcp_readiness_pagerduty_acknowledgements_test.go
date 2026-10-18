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
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyAcknowledgementsHTTP(t *testing.T) {
	previousIdentities := *apidserver.HostIdentitiesForUnseal
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return (*apidserver.MfaIdentities)() }
	t.Cleanup(func() { *apidserver.HostIdentitiesForUnseal = previousIdentities })
	e := apidserver.Setup(t, api.PlanPro)
	t.Cleanup(apidserver.WithTestRecipient(t))
	store := *e.Store()
	app := apidserver.SeedAppForTimeline(t, e, "pagerduty-ack")
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{ID: uuid.NewString(), AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "readiness", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	version := int64(0)
	one := int64(1)
	on, off := true, false
	path := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-pagerduty-acknowledgements"
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "/v1/webhooks/mcp-readiness/pagerduty/"+rule.ID) {
		t.Fatal("bootstrap URL missing", w.Code, w.Body.String())
	}
	primaryPath := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID + "/mcp-readiness-pagerduty"
	if w := e.Do(t, "PUT", primaryPath, api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: &version, Enabled: &on, IntegrationKey: strings.Repeat("a", 32), Region: "eu"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	secret := strings.Repeat("s", 32)
	request := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &version, Enabled: &on, ExpectedPagerDutyVersion: &one, ServiceID: "PSVC001", SigningSecret: secret}
	admin := *e.Key()
	reader, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = reader
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 403 {
		t.Fatal("reader mutated policy", w.Code)
	}
	*e.Key() = admin
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 || strings.Contains(w.Body.String(), secret) || !strings.Contains(w.Body.String(), `"region":"eu"`) {
		t.Fatal("admin binding", w.Code, w.Body.String())
	}
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 409 {
		t.Fatal("stale setting accepted", w.Code)
	}
	for _, suffix := range []string{"?unknown=1", "/events?limit=0", "/events?limit=1&limit=2", "/events?before=bad"} {
		if w := e.Do(t, "GET", path+suffix, nil, nil); w.Code != 400 {
			t.Fatal("query accepted", suffix, w.Code)
		}
	}
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"default"}})
	transition, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: at, Breached: true, Coverage: strings.Repeat("a", 64), Payload: payload, ObservedValue: 1})
	if err != nil {
		t.Fatal(err)
	}
	claim, won, err := store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won {
		t.Fatal(won, err)
	}
	if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), claim.EventID, claim.Token, at, state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{"event": map[string]any{"id": "event-http-1", "event_type": "incident.acknowledged", "resource_type": "incident", "occurred_at": time.Now().UTC(), "agent": map[string]string{"summary": "PRIVATE_ACTOR_EMAIL"}, "data": map[string]any{"id": "PINC001", "type": "incident", "status": "acknowledged", "incident_key": "gregale:mcp:" + transition.IncidentID, "self": "https://api.eu.pagerduty.com/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}}}
	raw, _ := json.Marshal(event)
	webhook := "/v1/webhooks/mcp-readiness/pagerduty/" + rule.ID
	post := func(body []byte, key string, headerOverride string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", webhook, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write(body)
		signature := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if headerOverride != "" {
			signature = headerOverride
		}
		r.Header.Set("X-PagerDuty-Signature", signature)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	if w := post(raw, "wrong-secret-123456", ""); w.Code != 401 {
		t.Fatal("unsigned ACK accepted", w.Code)
	}
	if w := post(raw, secret, ""); w.Code != 202 {
		t.Fatal("valid ACK rejected", w.Code, w.Body.String())
	}
	if w := post(raw, secret, ""); w.Code != 202 {
		t.Fatal("duplicate rejected", w.Code)
	}
	page, err := store.ListMCPReadinessAttention(t.Context(), rule.AccountID, app.ID, "", "", 20, true, time.Now())
	if err != nil || len(page.Incidents) != 1 || page.Incidents[0].State != "acknowledged" || page.Incidents[0].Acknowledgement.Source != "pagerduty" {
		t.Fatal(page, err)
	}
	// Signature rotation can include multiple signatures; unsupported events never resolve readiness.
	ignored := []byte(`{"event":{"event_type":"incident.resolved"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(ignored)
	if w := post(ignored, secret, "v1="+strings.Repeat("0", 64)+", v1="+hex.EncodeToString(mac.Sum(nil))); w.Code != 202 {
		t.Fatal(w.Code)
	}
	if w := post([]byte(`{"event":{"id":"one","ID":"two"}}`), secret, ""); w.Code != 400 {
		t.Fatal("ambiguous JSON accepted", w.Code)
	}
	if w := post(make([]byte, api.MCPReadinessPagerDutyWebhookBodyMaxBytes+1), secret, ""); w.Code != 413 {
		t.Fatal("oversized body accepted", w.Code)
	}
	*e.Key() = reader
	if w := e.Do(t, "GET", path+"/events", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "event-http-1") || strings.Contains(w.Body.String(), "PRIVATE_ACTOR_EMAIL") || strings.Contains(w.Body.String(), secret) {
		t.Fatal("read event history", w.Code, w.Body.String())
	}
	*e.Key() = admin
	request.ExpectedVersion = &one
	request.SigningSecret = strings.Repeat("n", 32)
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 {
		t.Fatal("secret rotation", w.Code, w.Body.String())
	}
	if w := post(raw, secret, ""); w.Code != 401 {
		t.Fatal("old signing key survived rotation", w.Code)
	}
	two := int64(2)
	if w := e.Do(t, "PUT", path, api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &two, Enabled: &off}, nil); w.Code != 200 {
		t.Fatal("disable", w.Code)
	}
	if w := post(raw, request.SigningSecret, ""); w.Code != 404 {
		t.Fatal("disabled webhook accepted", w.Code)
	}
	// A ciphertext from the outgoing integration namespace is never a webhook credential.
	wrongNamespace, err := secretbox.SealBytes((*apidserver.SetSecretRecipient)(), api.MCPReadinessPagerDutySecretNamespace, []byte(secret), api.MCPReadinessPagerDutyWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetMCPReadinessPagerDutyAcknowledgements(t.Context(), rule.AccountID, app.ID, rule.ID, 3, state.MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: wrongNamespace}); err != nil {
		t.Fatal(err)
	}
	if w := post(raw, secret, ""); w.Code != 503 {
		t.Fatal("foreign namespace accepted", w.Code)
	}
	// Pending MFA sessions cannot replay the administrative request.
	if err = store.MarkMFAEnrolled(t.Context(), rule.AccountID); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err = store.CreateSession(t.Context(), sid, rule.AccountID, "192.0.2.10", "ack-test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*(*e.S()).Sessions()).IssueWithSession(sid, rule.AccountID, true)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(request)
	r := httptest.NewRequest("PUT", path, strings.NewReader(string(b)))
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: token})
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("MFA bypass", w.Code)
	}
}
