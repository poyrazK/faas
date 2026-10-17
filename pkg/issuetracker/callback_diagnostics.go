package issuetracker

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type callbackRejection string

func (e callbackRejection) Error() string { return "tracker callback rejected: " + string(e) }
func (e callbackRejection) Unwrap() error { return ErrInvalidCallback }
func CallbackRejectionReason(err error) string {
	var rejection callbackRejection
	if errors.As(err, &rejection) {
		return string(rejection)
	}
	return "invalid_payload"
}

// SetupStatus describes observed delivery evidence, not provider registration.
func (h CallbackHealth) SetupStatus(now time.Time) string {
	if !h.Configured {
		return "Not configured"
	}
	if h.LastVerifiedAt == nil {
		return "Configured; awaiting verified delivery"
	}
	if h.LastVerifiedAt.After(now) {
		return "Configured; delivery evidence is ahead of Gregale’s clock"
	}
	if now.Sub(*h.LastVerifiedAt) > api.IssueTrackerCallbackVerificationMaxAge {
		return "Configured; delivery evidence is older than 24 hours"
	}
	return "Verified delivery received within 24 hours"
}
