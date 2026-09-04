// Mail retry decorator. Issue #246 acceptance item 3.
//
// RetryingSender wraps a Sender and retries on *TransientError
// (which both transports — pkg/mail/resend.go and pkg/mail/postmark.go
// — return for 429 and 5xx). The decorator uses full-jitter
// exponential backoff as the default delay, prefers the upstream's
// Retry-After when shorter, and never spends more than a hard
// wall-clock budget before returning the last error to the caller.
//
// All configuration is in pkg/api/limits.go (MailRetryMaxAttempts,
// MailRetryBaseDelayMS, MailRetryMaxWallClockMS) per the CLAUDE.md
// "every limit lives in limits.go" rule.
//
// Why synchronous, no background goroutine
// ----------------------------------------
// A fire-and-forget retry goroutine outlives the request context
// (the handler returns its HTTP response immediately while the
// goroutine keeps retrying for seconds or minutes). That makes
// the error path untestable in a request-scoped test harness, and
// worse, it makes the success/failure *indistinguishable* to the
// caller — a 5xx HTTP response cannot be issued because the
// goroutine hasn't reported back yet. Synchronous retry is the
// only shape that keeps the error semantics in-band with the
// handler's response. The hard wall-clock cap makes sure the
// handler never blocks longer than MailRetryMaxWallClockMS even
// in the worst case, which keeps tail latency predictable for
// every HTTP route that fires mail.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RetryingSender wraps a Sender and retries *TransientError returns
// with exponential backoff + full jitter, bounded by a hard
// wall-clock budget.
type RetryingSender struct {
	// Inner is the wrapped Sender. Required.
	Inner Sender
	// TransportName is the label used when calling
	// Metrics.RecordRetry (e.g. TransportResend, TransportPostmark).
	// Empty falls back to "unknown" so a misconfigured wiring is
	// visible in the metric rather than silently dropped.
	TransportName string
	// Log is the structured logger. nil falls back to slog.Default().
	Log *slog.Logger
	// Metrics is the optional observer. nil is safe (the seam is
	// already nil-tolerant; see pkg/mail/metrics.go).
	Metrics Metrics
	// Now is the clock. nil falls back to time.Now. Tests inject
	// a deterministic clock so the wall-clock-cap assertion is
	// reproducible.
	Now func() time.Time
	// Sleep blocks for d or until ctx is cancelled, whichever
	// comes first. nil falls back to a context-aware timer-based
	// sleep. Tests inject a recording stub so the suite does not
	// actually wait for MailRetryMaxWallClockMS per row.
	Sleep func(ctx context.Context, d time.Duration)
	// Backoff computes the default retry delay. nil uses the
	// production full-jitter exponential backoff. The seam lets
	// tests exercise Retry-After precedence deterministically without
	// depending on a random draw being larger than the provider hint.
	Backoff func(attempt int, base time.Duration) time.Duration
}

// Send retries the inner Sender on *TransientError. Non-transient
// errors are returned immediately without a second attempt; the
// upstream told us "do not retry", and respecting that is the
// difference between a 422 (bad address) and a 4xx-loop that
// eventually times out.
func (r *RetryingSender) Send(ctx context.Context, msg Message) error {
	inner := r.Inner
	if inner == nil {
		return errors.New("mail: RetryingSender.Inner is nil")
	}
	log := r.log()
	now := r.now()
	sleep := r.sleep()

	maxAttempts := api.MailRetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	maxWallClock := time.Duration(api.MailRetryMaxWallClockMS) * time.Millisecond
	deadline := now().Add(maxWallClock)

	for attempt := 1; ; attempt++ {
		err := inner.Send(ctx, msg)
		if err == nil {
			if attempt > 1 {
				log.Info("mail.retry.ok",
					"transport", r.transportLabel(),
					"attempt", attempt)
			}
			return nil
		}

		var te *TransientError
		if !errors.As(err, &te) {
			// Permanent error — upstream told us not to retry.
			// Returning here preserves the "respect the upstream"
			// contract: a 422 from Resend is not transient and a
			// retry would only waste an HTTP round-trip.
			return err
		}

		// No more attempts or no more wall-clock budget: stop
		// and surface the last transient error to the caller.
		if attempt >= maxAttempts {
			return err
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return err
		}

		delay := r.backoff()(attempt, time.Duration(api.MailRetryBaseDelayMS)*time.Millisecond)
		if te.RetryAfter > 0 && te.RetryAfter < delay {
			// Provider-supplied Retry-After is more honest than
			// our jitter; honour it when it's the shorter of the
			// two — a 2-second 429 hint beats a 1-second random
			// jitter, but a 30-second Retry-After loses to a 500ms
			// jitter so we don't block the handler forever.
			delay = te.RetryAfter
		}
		if delay > remaining {
			delay = remaining
		}

		if r.Metrics != nil {
			r.Metrics.RecordRetry(r.transportLabel())
		}
		log.Warn("mail.retry",
			"transport", r.transportLabel(),
			"attempt", attempt,
			"delay", delay,
			"status", te.Status,
			"retry_after", te.RetryAfter,
			"err", err)

		sleep(ctx, delay)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
}

// backoffDelay returns a full-jitter exponential backoff for the
// attempt index. attempt 1 → [0, base), attempt 2 → [0, base*2),
// attempt 3 → [0, base*4). Math/rand v2 is auto-seeded; no
// rand.New call needed.
func backoffDelay(attempt int, base time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	cap := base << (attempt - 1)
	if cap <= 0 {
		// Defensive: if a caller passes a base that overflows on
		// shift, fall back to the base itself so we never return
		// a negative duration.
		return base
	}
	return time.Duration(rand.Int64N(int64(cap)))
}

func (r *RetryingSender) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *RetryingSender) now() func() time.Time {
	if r.Now != nil {
		return r.Now
	}
	return time.Now
}

func (r *RetryingSender) sleep() func(context.Context, time.Duration) {
	if r.Sleep != nil {
		return r.Sleep
	}
	return func(ctx context.Context, d time.Duration) {
		if d <= 0 {
			return
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
}

func (r *RetryingSender) backoff() func(int, time.Duration) time.Duration {
	if r.Backoff != nil {
		return r.Backoff
	}
	return backoffDelay
}

func (r *RetryingSender) transportLabel() string {
	if r.TransportName != "" {
		return r.TransportName
	}
	return "unknown"
}
