// Package issuetriage handles native application-issue response controls.
package issuetriage

import (
	"github.com/onebox-faas/faas/pkg/state"
	"net/url"
	"strconv"
	"time"
)

// ParseAction accepts explicitly UTC dashboard times or RFC3339 offsets.
// The store checks authorization, revision and database-clock bounds.
func ParseAction(form url.Values) (int64, state.IssueApplicationTriageAction, error) {
	req := state.IssueApplicationTriageAction{Action: form.Get("action"), Reason: form.Get("reason")}
	revision, err := strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		return 0, req, state.ErrInvalidArgument
	}
	switch req.Action {
	case "acknowledge", "unsnooze", "snooze":
	default:
		return 0, req, state.ErrInvalidArgument
	}
	if value := form.Get("snoozed_until"); value != "" {
		until, err := time.Parse(time.RFC3339, value)
		if err != nil {
			until, err = time.Parse("2006-01-02T15:04", value)
		}
		if err != nil {
			return 0, req, state.ErrInvalidArgument
		}
		req.SnoozedUntil = &until
	}
	return revision, req, nil
}
