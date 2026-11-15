package state

import "time"

// CronTimedOutRunHold caps how long a timed-out cron run still counts as
// running for skip_if_running and the overlap policies.
//
// A run that outlives its request timeout is marked failed/timeout, but the
// app keeps running it: the gateway only stopped waiting. Counting only
// pending and dispatching rows let the next fire start beside it (prod
// 2026-11-15: skip_if_running did not skip a run that passed the timeout).
// A timed-out run therefore counts as active for as long again as it had
// already run, up to this cap, since the platform cannot see when the app
// finishes.
const CronTimedOutRunHold = 15 * time.Minute

// timedOutRunMayStillRun reports whether inv is a timed-out cron run that
// the app may still be running at now (see CronTimedOutRunHold).
func timedOutRunMayStillRun(inv Invocation, now time.Time) bool {
	if inv.State != InvocationFailed || inv.Outcome == nil || *inv.Outcome != OutcomeTimeout || inv.CompletedAt == nil {
		return false
	}
	hold := min(inv.CompletedAt.Sub(inv.CreatedAt), CronTimedOutRunHold)
	return now.Before(inv.CompletedAt.Add(hold))
}
