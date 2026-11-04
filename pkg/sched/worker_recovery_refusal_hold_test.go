// spec: §6.1
package sched_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// production-us hunt #14 (H14-98): a worker whose image serves HTTP was
// refused with execution_mode_conflict on every boot and retried every 300 s
// for 19 days (2,083 failed boots). A deterministic refusal holds an hour;
// transient failures keep the exponential backoff.
func TestWorkerRecoveryRetryDelayHoldsDeterministicRefusals(t *testing.T) {
	hold := time.Duration(api.WorkerRecoveryDeterministicRefusalHoldSeconds) * time.Second
	wrap := func(code string) error {
		return fmt.Errorf("reconcile worker environment default: %w", api.NewProblem(422, code, "refused", "detail"))
	}
	for _, tc := range []struct {
		name string
		err  error
		want time.Duration
	}{
		{"execution mode conflict", wrap(api.CodeExecutionModeConflict), hold},
		{"arch mismatch", wrap(api.CodeAppArchMismatch), hold},
		{"loopback bind", wrap(api.CodeAppLoopbackBound), hold},
		{"stateless only", wrap(api.CodeStatelessOnlyViolation), hold},
		{"startup crash keeps backoff", wrap(api.CodeAppStartupCrash), 5 * time.Minute},
		{"capacity keeps backoff", wrap(api.CodeCapacity), 5 * time.Minute},
		{"plain error keeps backoff", errors.New("dial vmmd"), 5 * time.Minute},
		{"no error keeps backoff", nil, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.WorkerRecoveryRetryDelay(10, tc.err); got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
	if got := sched.WorkerRecoveryRetryDelay(1, errors.New("x")); got != 5*time.Second {
		t.Fatalf("first transient retry = %v, want 5s", got)
	}
}
