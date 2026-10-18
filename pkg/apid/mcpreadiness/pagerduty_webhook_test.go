package mcpreadiness_test

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
	"github.com/onebox-faas/faas/pkg/apid/mcpreadiness"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessPagerDutyAcknowledgementIsolation(t *testing.T) {
	store := state.NewMemStore()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	type fixture struct{ account, app, rule, incident, secret string }
	seed := func(slug, key string) fixture {
		t.Helper()
		account, err := store.CreateAccount(t.Context(), slug+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := store.CreateAlertRule(t.Context(), state.AlertRule{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "readiness", Enabled: true, Metric: state.AlertMetric(api.MetricMCPReadinessUnhealthy), Comparison: state.AlertGt, WindowSpec: state.AlertWindow5m, Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", CooldownMinutes: 5})
		if err != nil {
			t.Fatal(err)
		}
		// The tenants deliberately share the same outgoing integration/service/region.
		sum := sha256.Sum256([]byte(strings.Repeat("a", 32)))
		if _, err = store.SetMCPReadinessPagerDuty(t.Context(), account.ID, app.ID, rule.ID, 0, state.MCPReadinessPagerDutyConfig{Enabled: true, Region: "eu", IntegrationKeySealed: []byte("sealed-native"), Fingerprint: hex.EncodeToString(sum[:])}); err != nil {
			t.Fatal(err)
		}
		sealed, err := secretbox.SealBytes(identity.Recipient(), api.MCPReadinessPagerDutyWebhookSecretNamespace, []byte(key), api.MCPReadinessPagerDutyWebhookSecretMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.SetMCPReadinessPagerDutyAcknowledgements(t.Context(), account.ID, app.ID, rule.ID, 0, state.MCPReadinessPagerDutyAcknowledgementConfig{Enabled: true, ExpectedPagerDutyVersion: 1, ServiceID: "PSVC001", SigningSecretSealed: sealed}); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"metric": api.MetricMCPReadinessUnhealthy, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"default"}})
		first, err := store.RecordMCPReadinessObservation(t.Context(), state.MCPReadinessObservation{Rule: rule, At: now, Breached: true, Coverage: strings.Repeat("a", 64), Payload: payload, ObservedValue: 1})
		if err != nil {
			t.Fatal(err)
		}
		claim, won, err := store.ClaimMCPReadinessDelivery(t.Context(), rule.ID, now)
		if err != nil || !won {
			t.Fatal(won, err)
		}
		if err = store.CompleteMCPReadinessProviderDelivery(t.Context(), claim.EventID, claim.Token, now, state.MCPReadinessProviderResult{Accepted: true, StatusCode: 202}); err != nil {
			t.Fatal(err)
		}
		return fixture{account.ID, app.ID, rule.ID, first.IncidentID, key}
	}
	a, b := seed("ack-isolation-a", strings.Repeat("s", 32)), seed("ack-isolation-b", strings.Repeat("t", 32))
	h := mcpreadiness.New(store, nil, nil)
	h.Identities = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webhooks/mcp-readiness/pagerduty/{id}", h.PagerDutyAcknowledgementWebhook)
	post := func(receiver, target fixture, key, eventID string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"event": map[string]any{"id": eventID, "event_type": "incident.acknowledged", "resource_type": "incident", "occurred_at": time.Now().UTC(), "data": map[string]any{"id": "PINC001", "type": "incident", "status": "acknowledged", "incident_key": "gregale:mcp:" + target.incident, "self": "https://api.eu.pagerduty.com/incidents/PINC001", "service": map[string]string{"id": "PSVC001"}}}})
		r := httptest.NewRequest("POST", "/v1/webhooks/mcp-readiness/pagerduty/"+receiver.rule, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write(body)
		r.Header.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := post(b, a, a.secret, "wrong-subscription"); code != 401 {
		t.Fatal("signing key not scoped to rule", code)
	}
	if code := post(a, b, a.secret, "foreign-incident"); code != 202 {
		t.Fatal(code)
	}
	for _, f := range []fixture{a, b} {
		page, err := store.ListMCPReadinessAttention(t.Context(), f.account, f.app, "", "", 20, true, time.Now())
		if err != nil || len(page.Incidents) != 1 || page.Incidents[0].State != "unacknowledged" {
			t.Fatal("cross-tenant acknowledgement", page, err)
		}
		events, err := store.ListMCPReadinessPagerDutyAcknowledgementEvents(t.Context(), f.account, f.app, f.rule, "", 20)
		if err != nil || len(events.Events) != 0 {
			t.Fatal("foreign event retained", events, err)
		}
	}
	if code := post(a, a, a.secret, "owned-incident"); code != 202 {
		t.Fatal(code)
	}
	events, err := store.ListMCPReadinessPagerDutyAcknowledgementEvents(t.Context(), a.account, a.app, a.rule, "", 20)
	if err != nil || len(events.Events) != 1 || events.Events[0].Region != "eu" || events.Events[0].ServiceID != "PSVC001" {
		t.Fatal(events, err)
	}
}
