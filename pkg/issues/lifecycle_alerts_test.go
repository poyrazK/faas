package issues

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLifecycleAlertDecision(t *testing.T) {
	now := time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC)
	last := now.Add(-59 * time.Second)
	for _, tc := range []struct {
		name   string
		policy api.IssueLifecycleAlertPolicy
		last   *time.Time
		at     time.Time
		reason string
	}{
		{"legacy defaults", api.IssueLifecycleAlertPolicy{}, &last, now, "eligible"},
		{"first alert", api.IssueLifecycleAlertPolicy{CooldownSeconds: 60}, nil, now, "eligible"},
		{"environment excluded", api.IssueLifecycleAlertPolicy{Environments: []string{"staging"}, CooldownSeconds: 60}, &last, now, "environment_excluded"},
		{"shared cooldown", api.IssueLifecycleAlertPolicy{Environments: []string{"production"}, CooldownSeconds: 60}, &last, now, "cooldown"},
		{"exact expiry", api.IssueLifecycleAlertPolicy{CooldownSeconds: 60}, &last, now.Add(time.Second), "eligible"},
		{"clock rollback", api.IssueLifecycleAlertPolicy{CooldownSeconds: 60}, &now, last, "cooldown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, next := LifecycleAlertDecision(tc.policy, "production", tc.last, tc.at)
			if reason != tc.reason || (next != nil) != (reason == "cooldown") {
				t.Fatalf("reason=%s next=%v", reason, next)
			}
			if next != nil && !next.Equal(tc.last.Add(60*time.Second)) {
				t.Fatalf("next eligibility=%v", next)
			}
		})
	}
}
