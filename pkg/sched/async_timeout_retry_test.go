package sched_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-15: a 45 s async job behind a 30 s request timeout was
// retried every ~32 s while earlier attempts still ran, 6 runs in all. A
// timed-out async invoke without its own policy now backs off from 30 s.
func TestAsyncTimeoutRetryBacksOff(t *testing.T) {
	timeout := fmt.Errorf("invoke: %w", sched.ErrDispatchTimeout)
	async := state.Invocation{Source: state.InvocationAsyncInvoke, Attempts: 1}
	if got := sched.AsyncTimeoutRetryDelay(async, timeout, 5*time.Second); got < 24*time.Second {
		t.Fatalf("first timeout retry = %s, want about 30 s", got)
	}
	async.Attempts = 3
	if got := sched.AsyncTimeoutRetryDelay(async, context.DeadlineExceeded, 5*time.Second); got < 96*time.Second {
		t.Fatalf("third timeout retry = %s, want about 120 s", got)
	}
	async.Attempts = 40
	if got := sched.AsyncTimeoutRetryDelay(async, timeout, 5*time.Second); got > 15*time.Minute {
		t.Fatalf("capped timeout retry = %s, want at most 15 min", got)
	}
	// prod 2026-11-19: apid stamps the plan's max_attempts on every async
	// invocation; that alone must not switch the timeout backoff off.
	stamped := state.Invocation{Source: state.InvocationAsyncInvoke, Attempts: 1, RetryPolicyJSON: []byte(`{"max_attempts":10}`)}
	if got := sched.AsyncTimeoutRetryDelay(stamped, timeout, 2*time.Second); got < 24*time.Second {
		t.Fatalf("timeout retry with the stamped plan max_attempts = %s, want about 30 s", got)
	}
	cases := []struct {
		name  string
		inv   state.Invocation
		err   error
		delay time.Duration
	}{
		{"declared delay curve", state.Invocation{Source: state.InvocationAsyncInvoke, Attempts: 1, RetryPolicyJSON: []byte(`{"max_attempts":3,"base_seconds":2}`)}, timeout, 5 * time.Second},
		{"other error", state.Invocation{Source: state.InvocationAsyncInvoke, Attempts: 1}, errors.New("connection reset"), 5 * time.Second},
		{"queue source", state.Invocation{Source: state.InvocationQueue, Attempts: 1}, timeout, 5 * time.Second},
		{"terminal failure", state.Invocation{Source: state.InvocationAsyncInvoke, Attempts: 1}, timeout, 0},
	}
	for _, tc := range cases {
		if got := sched.AsyncTimeoutRetryDelay(tc.inv, tc.err, tc.delay); got != tc.delay {
			t.Errorf("%s: delay = %s, want unchanged %s", tc.name, got, tc.delay)
		}
	}
}
