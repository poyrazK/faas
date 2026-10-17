package issues

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// LifecycleAlertDecision is shared by ingestion and draft previews. The cooldown
// spans created and regressed events for one canonical group, across all routes.
func LifecycleAlertDecision(policy api.IssueLifecycleAlertPolicy, environment string, last *time.Time, now time.Time) (string, *time.Time) {
	if len(policy.Environments) > 0 && !slices.Contains(policy.Environments, environment) {
		return "environment_excluded", nil
	}
	if last != nil && policy.CooldownSeconds > 0 {
		next := last.Add(time.Duration(policy.CooldownSeconds) * time.Second)
		if now.Before(next) {
			return "cooldown", &next
		}
	}
	return "eligible", nil
}
