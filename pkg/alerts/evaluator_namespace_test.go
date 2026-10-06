// ADR-045 — alert webhook secrets sealed by apid must open in the evaluator.

package alerts_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// TestEvaluator_DeliversApidSealedSecret reproduces production-us. apid seals
// every rule's webhook secret under "alert_rule_secret", and the evaluator
// rejected that namespace, so every customer alert ended "namespace mismatch:
// alert_rule_secret" with no webhook sent. The literal apid writes must
// dispatch. So must the legacy "alert_rule" namespace that fixtures used.
func TestEvaluator_DeliversApidSealedSecret(t *testing.T) {
	for _, namespace := range []string{"alert_rule_secret", "alert_rule"} {
		t.Run(namespace, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident, plaintext := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
			sealed, err := secretbox.SealBytes(ident.Recipient(), namespace, plaintext, 256)
			if err != nil {
				t.Fatalf("SealBytes: %v", err)
			}
			if _, err := store.UpdateAlertRule(context.Background(), rule.ID, state.UpdateAlertRuleParams{
				WebhookSecretSealed: &sealed,
			}); err != nil {
				t.Fatalf("UpdateAlertRule: %v", err)
			}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev, _ := makeEvaluator(t, store, &stubPromQL{value: 10}, ident, dispatch)
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.Delivered != 1 || dispatch.callCount() != 1 {
				t.Fatalf("stats = %+v, dispatch calls = %d; want one delivered webhook", stats, dispatch.callCount())
			}
		})
	}
}
