package main

import (
	"testing"
	"time"
)

// The helpers below shrink the CLI's real-time wait loops for one test and
// restore the production values on cleanup. They exist because the tests
// that pin those loops used to sleep through the production budgets (8 s
// cold-wake wait, 1 s poll backoffs), which is what made cmd/gregale the
// second-slowest package in CI. Only sequential tests may call them — Go
// runs t.Parallel tests after every sequential test has finished, so a
// package-level override never overlaps a parallel test as long as the
// caller itself does not call t.Parallel.

// fastOpenWake makes `gregale open`'s cold-wake wait settle in well under a
// second while keeping the same loop shape: probe, poll, deadline.
func fastOpenWake(t *testing.T) {
	t.Helper()
	probe, deadline, interval := openWakeProbeTimeout, openWakeDeadline, openWakePollInterval
	openWakeProbeTimeout = 500 * time.Millisecond
	openWakeDeadline = 400 * time.Millisecond
	openWakePollInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		openWakeProbeTimeout, openWakeDeadline, openWakePollInterval = probe, deadline, interval
	})
}

// fastBuildPoll shrinks pollBuildStatus's initial backoff so a three-step
// status sequence resolves in tens of milliseconds instead of seconds.
func fastBuildPoll(t *testing.T) {
	t.Helper()
	prev := buildPollInitialBackoff
	buildPollInitialBackoff = 20 * time.Millisecond
	t.Cleanup(func() { buildPollInitialBackoff = prev })
}

// fastLoginPoll shrinks waitForApproval's per-iteration backoff.
func fastLoginPoll(t *testing.T) {
	t.Helper()
	prev := loginPollBackoff
	loginPollBackoff = 20 * time.Millisecond
	t.Cleanup(func() { loginPollBackoff = prev })
}
