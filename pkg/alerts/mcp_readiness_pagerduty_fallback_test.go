package alerts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestMCPReadinessPagerDutyFallbackEvaluator(t *testing.T) {
	for _, region := range []string{"us", "eu"} {
		t.Run(region, func(t *testing.T) {
			base := state.NewMemStore()
			rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
			key := strings.Repeat("b", 32)
			sum := sha256.Sum256([]byte(key))
			sealed, err := secretbox.SealBytes(identity.Recipient(), api.MCPReadinessPagerDutySecretNamespace, []byte(key), api.MCPReadinessPagerDutyKeyBytes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = base.SetMCPReadinessFallback(t.Context(), rule.AccountID, rule.AppID, rule.ID, 0, state.MCPReadinessFallbackConfig{Enabled: true, Provider: "pagerduty", Region: region, IntegrationKeySealed: sealed, Fingerprint: hex.EncodeToString(sum[:]), AfterFailures: 1}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "default", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Hour)}}}
			var mu sync.Mutex
			var keys, actions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Key    string `json:"routing_key"`
					Dedup  string `json:"dedup_key"`
					Action string `json:"event_action"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Key != key {
					t.Error("fallback used wrong integration key")
				}
				mu.Lock()
				keys = append(keys, body.Dedup)
				actions = append(actions, body.Action)
				mu.Unlock()
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "dedup_key": body.Dedup})
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 503, Attempts: 1}}
			tick := func() {
				t.Helper()
				e := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog(), PagerDutyHTTPClient: &http.Client{Transport: pagerDutyTestTransport{target: target, base: server.Client().Transport, check: func(r *http.Request) {
					endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(region)
					if r.URL.String() != endpoint {
						t.Error("wrong fallback region", r.URL)
					}
				}}}})
				if _, err := e.RunOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			now = now.Add(time.Second)
			tick()
			now = now.Add(time.Second)
			source.statuses[0].ConsecutiveFailures = 0
			source.statuses[0].LastSuccessAt = &now
			tick()
			now = now.Add(time.Second)
			tick()
			mu.Lock()
			defer mu.Unlock()
			if len(keys) != 2 || keys[0] != keys[1] || strings.Join(actions, ",") != "trigger,resolve" {
				t.Fatal("fallback trigger/recovery lost identity", keys, actions)
			}
			if dispatcher.callCount() != 1 {
				t.Fatal("native fallback reached webhook dispatcher", dispatcher.callCount())
			}
			dispatcher.mu.Lock()
			incident, ok := dispatcher.calls[0].Payload["incident_id"].(string)
			dispatcher.mu.Unlock()
			if !ok || keys[0] != "gregale:mcp:"+incident {
				t.Fatal("failover changed incident dedup key")
			}
			deliveries, err := base.ListAlertDeliveriesForRule(t.Context(), rule.ID, 20, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range deliveries {
				if strings.Contains(string(d.Payload), key) || strings.Contains(d.LastError, key) {
					t.Fatal("integration credential exposed")
				}
			}
		})
	}
}
