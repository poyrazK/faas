package logsearch

// adr: 20261018083536 — a log_search_matches rule needs canonical filters,
// webhook delivery and a window of at most a day.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateAlertQuery(t *testing.T) {
	rollback, webhook := "rollback", "webhook"
	for _, tc := range []struct {
		name, metric, query, window string
		action                      *string
		enabled                     bool
		want                        string
		wantErr                     string
	}{
		{name: "canonicalized", metric: api.AlertRuleMetricLogSearchMatches, query: "q=timeout&level=error", window: "15m", action: &webhook, enabled: true, want: "level=error&q=timeout"},
		{name: "other metric without query", metric: "error_rate_pct", window: "7d", enabled: true},
		{name: "other metric with query", metric: "error_rate_pct", query: "level=error", window: "5m", enabled: true, wantErr: api.CodeAlertRuleInvalid},
		{name: "missing query", metric: api.AlertRuleMetricLogSearchMatches, window: "5m", enabled: true, wantErr: api.CodeAlertRuleInvalid},
		{name: "window key", metric: api.AlertRuleMetricLogSearchMatches, query: "since=1h", window: "5m", enabled: true, wantErr: api.CodeAlertRuleInvalid},
		{name: "rollback action", metric: api.AlertRuleMetricLogSearchMatches, query: "level=error", window: "5m", action: &rollback, enabled: true, wantErr: api.CodeAlertRuleInvalid},
		{name: "week window", metric: api.AlertRuleMetricLogSearchMatches, query: "level=error", window: "7d", enabled: true, wantErr: api.CodeAlertRuleInvalid},
		{name: "gate off", metric: api.AlertRuleMetricLogSearchMatches, query: "level=error", window: "5m", wantErr: "log_search_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, prob := ValidateAlertQuery(tc.metric, tc.query, tc.window, tc.action, tc.enabled)
			if tc.wantErr != "" {
				if prob == nil || prob.Code != tc.wantErr {
					t.Fatalf("problem = %+v, want %s", prob, tc.wantErr)
				}
				return
			}
			if prob != nil || got != tc.want {
				t.Fatalf("got %q, %+v; want %q", got, prob, tc.want)
			}
		})
	}
}
