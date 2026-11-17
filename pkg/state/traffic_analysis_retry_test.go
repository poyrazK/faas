package state

import (
	"context"
	"errors"
	"testing"
)

// An app delete failed outright when its traffic policy analysis ran past
// its 2 s budget during a database stall (prod 2026-11-09). Only the time
// budgets are retried, and only a bounded number of times.
func TestRetryTrafficAnalysisTimeout(t *testing.T) {
	timeout := &TrafficPolicyAnalysisError{Scope: "time", Unit: "milliseconds", Limit: 2000, Observed: 2001}
	for _, tc := range []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   error
	}{
		{"succeeds after a timeout", []error{timeout, nil}, 2, nil},
		{"database time also retries", []error{&TrafficPolicyAnalysisError{Scope: "database_time"}, nil}, 2, nil},
		{"bounded", []error{timeout, timeout, timeout, nil}, trafficAnalysisTimeoutAttempts, timeout},
		{"policy rejection is final", []error{&TrafficPolicyAnalysisError{Scope: "states"}}, 1, nil},
		{"other errors are final", []error{ErrConflict}, 1, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryTrafficAnalysisTimeout(context.Background(), func() error {
				err := tc.errs[calls]
				calls++
				return err
			})
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.name == "policy rejection is final" && err == nil {
				t.Fatal("a rejected policy was swallowed")
			}
		})
	}
}
