package alerts_test

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type fallbackCheckingDispatcher struct {
	t                 *testing.T
	primary, fallback string
	calls             []webhookout.Event
}

func (d *fallbackCheckingDispatcher) Dispatch(_ context.Context, target webhookout.Target, event webhookout.Event) webhookout.Result {
	d.t.Helper()
	expected := []byte("fallback-signing-key")
	if len(d.calls) == 0 {
		if target.URL != d.primary {
			d.t.Fatal("wrong primary target", target.URL)
		}
		expected = []byte("super-secret-shared-key")
	} else if target.URL != d.fallback {
		d.t.Fatal("wrong fallback target", target.URL)
	}
	body := []byte("signing-contract")
	signature := webhookout.NewSigner(expected).Sign(1, event.ID, body)
	if target.Signer.Verify(1, event.ID, body, signature) != nil {
		d.t.Fatal("destination used another receiver's secret")
	}
	d.calls = append(d.calls, event)
	code := 200
	if len(d.calls) == 1 {
		code = 503
	}
	return webhookout.Result{StatusCode: code, Attempts: 1}
}
func TestMCPReadinessFallbackEvaluator(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	sealed, err := secretbox.SealBytes(identity.Recipient(), alerts.AlertSecretNamespace, []byte("fallback-signing-key"), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	fallbackURL := "https://fallback.example.com/hook"
	if _, err = base.SetMCPReadinessFallback(t.Context(), rule.AccountID, rule.AppID, rule.ID, 0, state.MCPReadinessFallbackConfig{Enabled: true, WebhookURL: fallbackURL, WebhookSecretSealed: sealed, AfterFailures: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "default", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Hour)}}}
	dispatcher := &fallbackCheckingDispatcher{t: t, primary: rule.WebhookURL, fallback: fallbackURL}
	tick := func() {
		t.Helper()
		evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog()})
		if _, err := evaluator.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	now = now.Add(time.Second)
	tick()
	if len(dispatcher.calls) != 2 || dispatcher.calls[0].ID != dispatcher.calls[1].ID || dispatcher.calls[0].Payload["incident_id"] != dispatcher.calls[1].Payload["incident_id"] {
		t.Fatal("failover changed logical event", dispatcher.calls)
	}
	now = now.Add(time.Second)
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].LastSuccessAt = &now
	tick()
	if len(dispatcher.calls) != 3 || dispatcher.calls[2].Payload["event"] != "alert.resolved" || dispatcher.calls[2].Payload["incident_id"] != dispatcher.calls[0].Payload["incident_id"] {
		t.Fatal("recovery did not follow failure", dispatcher.calls)
	}
	now = now.Add(time.Second)
	tick()
	if len(dispatcher.calls) != 3 {
		t.Fatal("terminal delivery repeated")
	}
}
