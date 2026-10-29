package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// CronFireNowInvocationStore links HTTP fire-now requests to the invocation
// the drain delivers (ADR-20261026201726). A queued request stays running in
// cron_fire_now_requests; GetFireNowRequest reports the terminal status of
// its invocation, so no settle step can race the drain.
type CronFireNowInvocationStore interface {
	// InsertFireNowRequestUnderCap inserts a pending request unless the
	// cron already has maxActive requests waiting or running that were
	// requested at or after since. At the cap it returns *FireNowCapError.
	InsertFireNowRequestUnderCap(ctx context.Context, cronID, accountID string, maxActive int, since time.Time) (string, error)
	// MarkFireNowRequestQueued records the invocation a running request
	// queued. ErrFireNowRequestNotFound when the request is not running.
	MarkFireNowRequestQueued(ctx context.Context, requestID, invocationID string) error
}

// FireNowCapError reports a cron at its active fire-now request cap.
type FireNowCapError struct {
	Limit    int
	Observed int
}

func (e *FireNowCapError) Error() string {
	return fmt.Sprintf("state: cron has %d active fire-now requests (limit %d)", e.Observed, e.Limit)
}

// Is lets callers treat the cap as ErrQuotaExceeded.
func (e *FireNowCapError) Is(target error) bool { return target == ErrQuotaExceeded }

// fireNowInvocationActive reports whether an invocation state still counts a
// queued fire-now request as active.
func fireNowInvocationActive(state InvocationState) bool {
	return state == InvocationPending || state == InvocationDispatching
}

// settleFireNowFromInvocation reports a running request whose invocation has
// reached a terminal state with that outcome. Other requests are unchanged;
// so is a request whose invocation row is gone (retention).
func settleFireNowFromInvocation(req FireNowRequest, state InvocationState, lastError *string, completedAt *time.Time) FireNowRequest {
	if req.Status != FireNowStatusRunning || req.InvocationID == nil || state == "" || fireNowInvocationActive(state) {
		return req
	}
	switch state {
	case InvocationCompleted:
		req.Status = FireNowStatusSucceeded
	case InvocationCancelled:
		req.Status = FireNowStatusCancelled
	default:
		req.Status = FireNowStatusFailed
		message := "invocation " + string(state)
		if lastError != nil && *lastError != "" {
			message = *lastError
		}
		message = safetext.Truncate(message, api.AuditReasonMaxBytes)
		req.Error = &message
	}
	if completedAt != nil && !completedAt.IsZero() {
		finished := completedAt.UTC()
		req.FinishedAt = &finished
	}
	return req
}
