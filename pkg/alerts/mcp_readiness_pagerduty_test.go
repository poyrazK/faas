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
)

type pagerDutyTestTransport struct {
	target *url.URL
	base   http.RoundTripper
	check  func(*http.Request)
}

func (r pagerDutyTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.check != nil {
		r.check(req)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func TestMCPReadinessPagerDutyEvaluator(t *testing.T) {
	for _, region := range []string{"us", "eu"} {
		for _, namespace := range []string{api.MCPReadinessPagerDutySecretNamespace, alerts.AlertSecretNamespace} {
			t.Run(region+"/"+namespace, func(t *testing.T) {
				base := state.NewMemStore()
				rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
				key := strings.Repeat("a", 32)
				sum := sha256.Sum256([]byte(key))
				sealed, e := secretbox.SealBytes(identity.Recipient(), namespace, []byte(key), api.MCPReadinessPagerDutyKeyBytes)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = base.SetMCPReadinessPagerDuty(t.Context(), rule.AccountID, rule.AppID, rule.ID, 0, state.MCPReadinessPagerDutyConfig{Region: region, Enabled: true, IntegrationKeySealed: sealed, Fingerprint: hex.EncodeToString(sum[:])}); e != nil {
					t.Fatal(e)
				}
				if _, e = base.SetMCPReadinessReminderDefaults(t.Context(), rule.AccountID, rule.AppID, rule.ID, 0, api.MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 1}); e != nil {
					t.Fatal(e)
				}
				now := time.Now().UTC()
				source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "default", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Hour)}}}
				var captured sync.Mutex
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
						t.Error("incorrect credential")
					}
					captured.Lock()
					keys = append(keys, body.Dedup)
					actions = append(actions, body.Action)
					captured.Unlock()
					w.WriteHeader(202)
					_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "dedup_key": body.Dedup})
				}))
				defer server.Close()
				target, _ := url.Parse(server.URL)
				dispatcher := &recordingDispatcher{}
				evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog(), PagerDutyHTTPClient: &http.Client{Transport: pagerDutyTestTransport{target: target, base: server.Client().Transport, check: func(r *http.Request) {
					endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(region)
					if r.URL.String() != endpoint {
						t.Error("wrong regional endpoint", r.URL)
					}
				}}}})
				tick := func() {
					t.Helper()
					if _, e := evaluator.RunOnce(t.Context()); e != nil {
						t.Fatal(e)
					}
				}
				tick()
				now = now.Add(time.Minute)
				tick()
				now = now.Add(time.Second)
				source.statuses[0].ConsecutiveFailures = 0
				source.statuses[0].LastSuccessAt = &now
				tick()
				captured.Lock()
				defer captured.Unlock()
				if namespace == api.MCPReadinessPagerDutySecretNamespace {
					if len(keys) != 3 || keys[0] != keys[1] || keys[0] != keys[2] || strings.Join(actions, ",") != "trigger,trigger,resolve" {
						t.Fatal(keys, actions)
					}
				} else if len(keys) != 0 {
					t.Fatal("foreign namespace credential dispatched")
				}
				if dispatcher.callCount() != 0 {
					t.Fatal("native event reached webhook dispatcher")
				}
				deliveries, e := base.ListAlertDeliveriesForRule(t.Context(), rule.ID, 20, false)
				if e != nil {
					t.Fatal(e)
				}
				for _, v := range deliveries {
					if strings.Contains(v.LastError, key) || strings.Contains(string(v.Payload), key) {
						t.Fatal("credential leaked")
					}
				}
			})
		}
	}
}
