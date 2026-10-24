package conformance

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLogSearchAlertRuleRoundTrips pins ADR-20261018083536 slice D2: a
// log_search_matches rule stores its own log_query and reads it back, and
// other rules carry none.
func testLogSearchAlertRuleRoundTrips(t *testing.T, fx *Fixture) {
	t.Helper()
	rule := state.AlertRule{
		AccountID: fx.Account.ID, AppID: fx.App.ID, Name: "log-search-" + uuid.NewString(), Enabled: true,
		Metric: state.AlertMetricLogSearchMatches, Comparison: state.AlertComparison("gt"), Threshold: 20,
		WindowSpec: state.AlertWindow15m, Action: state.AlertAction("webhook"), WebhookURL: "https://example.test/hook",
		WebhookSecretSealed: []byte("sealed-conformance"), CooldownMinutes: 15, State: state.AlertState("ok"),
		LogQuery: "level=error&q=timeout&source=runtime",
	}
	created, err := fx.Store.CreateAlertRule(fx.Ctx, rule)
	if err != nil {
		t.Fatalf("CreateAlertRule(log_search_matches): %v", err)
	}
	got, err := fx.Store.AlertRuleByID(fx.Ctx, created.ID)
	if err != nil || got.Metric != state.AlertMetricLogSearchMatches || got.LogQuery != rule.LogQuery || got.WindowSpec != state.AlertWindow15m {
		t.Fatalf("AlertRuleByID = %+v, %v; want the stored log_query", got, err)
	}
	plain := rule
	plain.Name, plain.Metric, plain.LogQuery = "plain-"+uuid.NewString(), state.AlertMetric("error_rate_pct"), ""
	created, err = fx.Store.CreateAlertRule(fx.Ctx, plain)
	if err != nil {
		t.Fatalf("CreateAlertRule(error_rate_pct): %v", err)
	}
	if got, err := fx.Store.AlertRuleByID(fx.Ctx, created.ID); err != nil || got.LogQuery != "" {
		t.Fatalf("plain rule = %+v, %v; want no log_query", got, err)
	}
}
