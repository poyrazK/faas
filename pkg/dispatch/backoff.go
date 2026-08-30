package dispatch

import (
	"math/rand/v2"
	"time"
)

// Backoff returns the retry delay for the given attempt number
// (1-indexed: attempt=1 is the first retry after the initial
// dispatch; attempt=0 is treated as 1). The curve is:
//
//	base      = min(2^(attempt-1), p.MaxSeconds)
//	jitter    = ±(p.JitterSeconds * base)        // symmetric, fraction of base
//	return base + jitter
//
// attempt < 1 is clamped to 1 (the inline curve at
// pkg/sched/dispatch_triggers.go:1009 has the same clamp).
// attempt > 1 + log2(MaxSeconds) clamps the base at MaxSeconds;
// the exponential exponent itself is capped at 9 to keep
// time.Duration's int64 shift in range.
//
// JitterSeconds is a *fraction* of base (e.g. 0.2 = ±20%),
// matching the inline curve exactly: every retry delay falls
// within [base * (1 - JitterSeconds), base * (1 + JitterSeconds)].
// The inline curve has used ±20% since Move 1; this parameter
// makes the window explicit per-policy so PR-B's per-row JSONB
// overrides can express "no jitter" (JitterSeconds=0) or
// "wider jitter for a flaky downstream" without forking the
// dispatcher.
//
// On a Zero() policy, Backoff returns 0 — the dispatcher should
// retry as soon as it can, and rely on the producer's attempt
// budget (e.g. api.Limits.MaxQueueAttempts) for the retry cap.
//
// The jitter is computed with math/rand/v2 — see the inline
// gosec: G404 waiver at dispatch_triggers.go:1021 for the
// rationale: adversarial callers can nudge our delay by at most
// JitterSeconds of base, which the dispatch tick absorbs.
func (p RetryPolicy) Backoff(attempt int) time.Duration {
	if p.Zero() {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}

	// Mirror the inline curve's exp clamp. The original caps at 9
	// because time.Second << 10 already exceeds int64 range
	// comfortably; the explicit clamp keeps the intent obvious.
	exp := attempt - 1
	if exp > 9 {
		exp = 9
	}

	baseSeconds := float64(uint64(1) << exp) // 2^exp
	if max := p.MaxSeconds; max > 0 && baseSeconds > max {
		baseSeconds = max
	}

	// Jitter: symmetric ±JitterSeconds fraction of base. Use a
	// 41-bucket table (0..40 → -1.0..+1.0 in steps of 0.05) to
	// match the inline curve's discrete steps exactly. A uniform
	// float would also be acceptable, but the inline curve is
	// the load-bearing SLO for cron / queue retry and we keep
	// the bucket count stable so callers' dashboards don't drift.
	jitter := 0.0
	if js := p.JitterSeconds; js > 0 {
		jitter = (float64(rand.Uint64()%41) - 20) / 20 * js * baseSeconds //nolint:gosec // G404: see ADR-134 §6.7
	}

	return time.Duration((baseSeconds + jitter) * float64(time.Second))
}
