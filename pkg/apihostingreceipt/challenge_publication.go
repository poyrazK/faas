package apihostingreceipt

import (
	"context"
	"time"
)

type challengePublicationDeadlineKey struct{}

// WithChallengePublicationDeadline bounds publication during durable recovery.
// Once publication succeeds, the candidate probe keeps its normal timeout and
// the caller's cancellation. The deadline is deliberately not a parent timeout.
func WithChallengePublicationDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, challengePublicationDeadlineKey{}, deadline)
}

func challengePublicationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	deadline, _ := ctx.Value(challengePublicationDeadlineKey{}).(time.Time)
	if timeout > 0 {
		requestDeadline := time.Now().Add(timeout)
		if deadline.IsZero() || requestDeadline.Before(deadline) {
			deadline = requestDeadline
		}
	}
	if !deadline.IsZero() {
		return context.WithDeadline(ctx, deadline)
	}
	return ctx, func() {}
}

// ChallengePublicationError means no candidate request was sent because the
// platform could not publish its challenge. Configuration failures remain
// ordinary failed verdicts. Error intentionally omits the underlying error:
// publishers can include the secret challenge in their diagnostics.
type ChallengePublicationError struct{ Cause error }

func (*ChallengePublicationError) Error() string {
	return "platform smoke challenge publication is temporarily unavailable"
}

func (e *ChallengePublicationError) Unwrap() error { return e.Cause }
