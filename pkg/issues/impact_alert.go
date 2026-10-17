package issues

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ImpactAlertCrossesThreshold excludes crossings during an active ignore period.
// Collection continues while ignored; ending that period does not replay crossings.
func ImpactAlertCrossesThreshold(issue api.Issue, minimum, before, after int64, now time.Time) bool {
	if minimum <= 0 || before >= minimum || after < minimum {
		return false
	}
	return issue.State != "ignored" || (issue.IgnoredUntil != nil && !issue.IgnoredUntil.After(now))
}
