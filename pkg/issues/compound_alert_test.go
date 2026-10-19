// adr: 1662
package issues

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCompoundAlertDecisionAndStableIdentity(t *testing.T) {
	now := time.Now().UTC()
	rule := api.IssueCompoundAlertRule{Key: "production-impact", Enabled: true, Environment: "production", MinimumEvents: 20, MinimumCustomers: 3, WindowSeconds: 600, CooldownSeconds: 1800}
	base := api.Issue{ID: "issue", Environment: "production", State: "open"}
	last := now.Add(-time.Minute)
	for _, tt := range []struct {
		name              string
		events, customers int64
		snoozed           bool
		last              *time.Time
		state, env        string
		reason            string
	}{
		{"eligible", 20, 3, false, nil, "open", "production", "eligible"},
		{"frequency-only", 20, 2, false, nil, "open", "production", "customer_threshold"},
		{"impact-only", 19, 3, false, nil, "open", "production", "event_threshold"},
		{"environment", 20, 3, false, nil, "open", "staging", "environment_excluded"},
		{"ignored", 20, 3, false, nil, "ignored", "production", "suppressed"},
		{"snoozed", 20, 3, true, nil, "open", "production", "suppressed"},
		{"cooldown", 20, 3, false, &last, "open", "production", "cooldown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := base
			i.State, i.Environment = tt.state, tt.env
			got := CompoundAlertDecision(rule, i, tt.events, tt.customers, tt.snoozed, tt.last, now)
			if got.Reason != tt.reason || got.Allowed != (tt.reason == "eligible") || !got.WindowStart.Equal(now.Add(-600*time.Second)) {
				t.Fatal(got)
			}
		})
	}
	copy := rule
	copy.Enabled = false
	if CompoundRuleHash(copy) != CompoundRuleHash(rule) {
		t.Fatal("disable reset cooldown identity")
	}
	copy.MinimumCustomers++
	if CompoundRuleHash(copy) == CompoundRuleHash(rule) {
		t.Fatal("customer condition omitted from cooldown identity")
	}
	expired := now.Add(-time.Second)
	base.State = "ignored"
	base.IgnoredUntil = &expired
	last = now.Add(-1800 * time.Second)
	if got := CompoundAlertDecision(rule, base, 20, 3, false, &last, now); !got.Allowed {
		t.Fatal("ignore/cooldown boundary", got)
	}
}
