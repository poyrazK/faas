package issuetrackers

import (
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ParseTriageAction accepts the dashboard's explicitly UTC datetime input and
// RFC3339 values. Bounds and the current revision are checked in the store.
func ParseTriageAction(form url.Values) (state.IssueTrackerTriageAction, error) {
	req := state.IssueTrackerTriageAction{Action: form.Get("action"), Reason: form.Get("reason")}
	value := form.Get("snoozed_until")
	if value != "" {
		until, err := time.Parse(time.RFC3339, value)
		if err != nil {
			until, err = time.Parse("2006-01-02T15:04", value)
		}
		if err != nil {
			return req, state.ErrInvalidArgument
		}
		req.SnoozedUntil = &until
	}
	return req, nil
}
