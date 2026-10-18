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
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyReleaseHTTP(t *testing.T) {
	previous := *apidserver.HostIdentitiesForUnseal
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return (*apidserver.MfaIdentities)() }
	t.Cleanup(func() { *apidserver.HostIdentitiesForUnseal = previous })
	e := apidserver.Setup(t, api.PlanPro)
	t.Cleanup(apidserver.WithTestRecipient(t))
	store := *e.Store()
	account := (*e.Acct()).ID
	app := apidserver.SeedAppForTimeline(t, e, "release-sync")
	rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{AccountID: account, AppID: app.ID, Name: "readiness", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + app.Slug + "/alerts/" + rule.ID
	path := base + "/mcp-readiness-pagerduty-acknowledgements"
	zero, one := int64(0), int64(1)
	on := true
	secret := strings.Repeat("s", 32)
	if w := e.Do(t, "PUT", base+"/mcp-readiness-pagerduty", api.SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32)}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	request := api.SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &zero, Enabled: &on, ExpectedPagerDutyVersion: &one, ServiceID: "PSVC001", SigningSecret: secret}
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	at := time.Now().UTC().Add(-5 * time.Second).Truncate(time.Microsecond)
	payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"default"}})
	opened, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: at, Breached: true, Coverage: strings.Repeat("a", 64), Payload: payload, ObservedValue: 1})
	if err != nil {
		t.Fatal(err)
	}
	claim, won, err := store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, at)
	if err != nil || !won {
		t.Fatal(won, err)
	}
	if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), claim.EventID, claim.Token, at, state.MCPReadinessProviderResult{StatusCode: 202, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	post := func(id, action, status, key string, occur time.Time) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"event": map[string]any{"id": id, "event_type": action, "resource_type": "incident", "occurred_at": occur, "data": map[string]any{"id": "PINC001", "type": "incident", "status": status, "incident_key": "gregale:mcp:" + opened.IncidentID, "self": "https://api.pagerduty.com/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}}})
		r := httptest.NewRequest("POST", "/v1/webhooks/mcp-readiness/pagerduty/"+rule.ID+"/primary", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write(body)
		r.Header.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	view := func() api.MCPReadinessIncidentAttention {
		t.Helper()
		page, err := store.ListMCPReadinessAttention(t.Context(), account, app.ID, "", "", 20, true, time.Now())
		if err != nil || len(page.Incidents) != 1 {
			t.Fatal(page, err)
		}
		return page.Incidents[0]
	}
	if w := post("legacy-ack", "incident.acknowledged", "acknowledged", secret, at.Add(time.Second)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post("disabled-release", "incident.unacknowledged", "triggered", secret, at.Add(2*time.Second)); w.Code != 202 || view().State != "acknowledged" {
		t.Fatal(w.Code, w.Body.String())
	}
	request.ExpectedVersion = &one
	request.SyncUnacknowledgements = true
	if w := e.Do(t, "PUT", path, request, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"sync_unacknowledgements":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post("legacy-protected", "incident.unacknowledged", "triggered", secret, at.Add(3*time.Second)); w.Code != 202 || view().State != "acknowledged" {
		t.Fatal("legacy ownership adopted", w.Code, w.Body.String())
	}
	v := view()
	local := time.Now().UTC()
	if _, err = store.ActMCPReadinessAttention(t.Context(), account, app.ID, opened.IncidentID, api.MCPReadinessAttentionAction{RequestID: uuid.NewString(), ExpectedVersion: &v.Version, Action: "release", Reason: "Begin tracked provider cycle"}, local); err != nil {
		t.Fatal(err)
	}
	if w := post("tracked-ack", "incident.acknowledged", "acknowledged", secret, local.Add(time.Second)); w.Code != 202 || len(view().ProviderAcknowledgements) != 1 {
		t.Fatal("tracked ownership missing", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		id, status, key string
		want            int
	}{{"bad-signature", "triggered", strings.Repeat("x", 32), 401}, {"bad-state", "acknowledged", secret, 400}, {"tracked-ack", "triggered", secret, 409}} {
		if w := post(tc.id, "incident.unacknowledged", tc.status, tc.key, local.Add(2*time.Second)); w.Code != tc.want {
			t.Fatal(tc.id, w.Code, w.Body.String())
		}
	}
	if w := post("tracked-release", "incident.unacknowledged", "triggered", secret, local.Add(2*time.Second)); w.Code != 202 || view().Acknowledgement != nil {
		t.Fatal("release failed", w.Code, w.Body.String())
	}
	version := view().Version
	if w := post("tracked-release", "incident.unacknowledged", "triggered", secret, local.Add(2*time.Second)); w.Code != 202 || view().Version != version {
		t.Fatal("duplicate release reapplied", w.Code)
	}
	w := e.Do(t, "GET", path+"/events", nil, nil)
	var events api.MCPReadinessPagerDutyAcknowledgementEvents
	if json.Unmarshal(w.Body.Bytes(), &events) != nil {
		t.Fatal(w.Body.String())
	}
	released := false
	for _, event := range events.Events {
		if event.Outcome == "released" && event.Action == "release" && event.Destination == "primary" {
			released = true
		}
	}
	if !released {
		t.Fatal("release history missing", w.Body.String())
	}
}
