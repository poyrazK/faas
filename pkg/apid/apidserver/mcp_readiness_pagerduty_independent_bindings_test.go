package apidserver_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyIndependentBindingsHTTP(t *testing.T) {
	previous := *apidserver.HostIdentitiesForUnseal
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return (*apidserver.MfaIdentities)() }
	t.Cleanup(func() { *apidserver.HostIdentitiesForUnseal = previous })
	e := apidserver.Setup(t, api.PlanPro)
	t.Cleanup(apidserver.WithTestRecipient(t))
	store := *e.Store()
	app := apidserver.SeedAppForTimeline(t, e, "independent-ack")
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "readiness", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID
	path := base + "/mcp-readiness-pagerduty-acknowledgements"
	zero, one := int64(0), int64(1)
	on, off := true, false
	if w := e.Do(t, "PUT", base+"/mcp-readiness-pagerduty", api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32)}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := e.Do(t, "PUT", base+"/mcp-readiness-fallback", api.SetMCPReadinessFallbackRequest{ExpectedVersion: &zero, Enabled: &on, Provider: "pagerduty", IntegrationKey: strings.Repeat("b", 32), AfterFailures: 1}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	secrets := map[string]string{"primary": strings.Repeat("p", 32), "fallback": strings.Repeat("f", 32)}
	for _, d := range []string{"fallback", "primary"} {
		request := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{Destination: d, ExpectedVersion: &zero, Enabled: &on, ServiceID: "PSVC001", SigningSecret: secrets[d]}
		if d == "primary" {
			request.ExpectedPagerDutyVersion = &one
		} else {
			request.ExpectedFallbackVersion = &one
		}
		if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 {
			t.Fatal(d, w.Code, w.Body.String())
		}
	}
	for _, d := range []string{"primary", "fallback"} {
		w := e.Do(t, "GET", path+"?destination="+d, nil, nil)
		var p api.MCPReadinessPagerDutyAcknowledgementPolicy
		if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || p.Version != 1 || p.Destination != d || !strings.HasSuffix(p.WebhookPath, "/"+d) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"?destination=other", "?destination=", "?destination=primary&destination=fallback", "?destination=primary&unknown=1", "?destination=%zz"} {
		if w := e.Do(t, "GET", path+query, nil, nil); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"default"}})
	opened, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: at, Breached: true, Coverage: strings.Repeat("a", 64), Payload: payload, ObservedValue: 1})
	if err != nil {
		t.Fatal(err)
	}
	claim, won, err := store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won {
		t.Fatal(won, err)
	}
	if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), claim.EventID, claim.Token, at, state.MCPReadinessProviderResult{StatusCode: 503, Failure: "provider_rejected"}); err != nil {
		t.Fatal(err)
	}
	claim, won, err = store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won || claim.Delivery.ReadinessDestination != "fallback" {
		t.Fatal(won, err)
	}
	if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), claim.EventID, claim.Token, at, state.MCPReadinessProviderResult{StatusCode: 202, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{"event": map[string]any{"id": "independent-http-event", "event_type": "incident.acknowledged", "resource_type": "incident", "occurred_at": time.Now().UTC(), "data": map[string]any{"id": "PINC001", "type": "incident", "status": "acknowledged", "incident_key": "gregale:mcp:" + opened.IncidentID, "self": "https://api.pagerduty.com/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}}}
	raw, _ := json.Marshal(event)
	callback := "/v1/webhooks/mcp-readiness/pagerduty/" + rule.ID
	post := func(suffix, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", callback+suffix, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write(raw)
		r.Header.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		suffix, key string
		status      int
	}{
		{"", secrets["primary"], 401}, {"/primary", secrets["fallback"], 401}, {"/fallback", secrets["primary"], 401},
		{"/primary", secrets["primary"], 202}, {"/fallback", secrets["fallback"], 202}, {"", secrets["fallback"], 202},
	} {
		if w := post(tc.suffix, tc.key); w.Code != tc.status {
			t.Fatal(tc.suffix, w.Code, w.Body.String())
		}
	}
	w := e.Do(t, "GET", path+"/events", nil, nil)
	var events api.MCPReadinessPagerDutyAcknowledgementEvents
	if json.Unmarshal(w.Body.Bytes(), &events) != nil || len(events.Events) != 1 || events.Events[0].Destination != "primary" {
		t.Fatal("duplicate callbacks changed history", w.Body.String())
	}
	// Rotate primary only. Existing fallback and original fallback URLs keep their own secret.
	rotated := strings.Repeat("r", 32)
	if w := e.Do(t, "PUT", path, api.SetMCPReadinessPagerDutyAcknowledgementsRequest{Destination: "primary", ExpectedVersion: &one, Enabled: &on, ExpectedPagerDutyVersion: &one, ServiceID: "PSVC001", SigningSecret: rotated}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		suffix, key string
		status      int
	}{{"/primary", secrets["primary"], 401}, {"/primary", rotated, 202}, {"/fallback", secrets["fallback"], 202}, {"", secrets["fallback"], 202}} {
		if w := post(tc.suffix, tc.key); w.Code != tc.status {
			t.Fatal(tc.suffix, w.Code, w.Body.String())
		}
	}
	// Legacy disable still selects the original fallback binding.
	if w := e.Do(t, "PUT", path, api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &one, Enabled: &off}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post("/fallback", secrets["fallback"]); w.Code != 404 {
		t.Fatal("fallback not revoked", w.Code)
	}
	if w := post("", secrets["fallback"]); w.Code != 404 {
		t.Fatal("legacy URL changed destinations", w.Code)
	}
	if w := post("/primary", rotated); w.Code != 202 {
		t.Fatal("fallback disable affected primary", w.Code, w.Body.String())
	}
}
