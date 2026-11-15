package state

import (
	"testing"
	"time"
)

// prod 2026-11-15: skip_if_running let a new fire start beside a run that
// had passed its request timeout and was still running in the app.
func TestTimedOutCronRunStillCountsAsRunning(t *testing.T) {
	created := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	completed := created.Add(30 * time.Second)
	timeout, failed := OutcomeTimeout, OutcomeFailed
	run := Invocation{State: InvocationFailed, Outcome: &timeout, CreatedAt: created, CompletedAt: &completed}
	if !timedOutRunMayStillRun(run, completed.Add(29*time.Second)) {
		t.Fatal("a run 30 s past its timeout was not held for another 30 s")
	}
	if timedOutRunMayStillRun(run, completed.Add(31*time.Second)) {
		t.Fatal("a timed-out run was held longer than it had run")
	}
	long := completed.Add(2 * time.Hour)
	run.CompletedAt = &long
	if timedOutRunMayStillRun(run, long.Add(CronTimedOutRunHold+time.Second)) {
		t.Fatal("hold not capped at CronTimedOutRunHold")
	}
	run.CompletedAt, run.Outcome = &completed, &failed
	if timedOutRunMayStillRun(run, completed.Add(time.Second)) {
		t.Fatal("a plain failure counted as still running")
	}
}
