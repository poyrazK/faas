package state

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRetryRuntimeAdmissionBusy(t *testing.T) {
	rawBusy := &pgconn.PgError{Code: "55P03", ConstraintName: "application_standard_runtime_busy"}
	for _, tc := range []struct {
		name     string
		failures []error
		want     error
		wantPg   bool
		attempts int
	}{
		{name: "mapped busy then commit", failures: []error{ErrApplicationStandardRuntimeBusy, ErrApplicationStandardRuntimeBusy}, attempts: 3},
		{name: "raw guard busy then commit", failures: []error{fmt.Errorf("insert: %w", rawBusy)}, attempts: 2},
		{name: "definitive refusal", failures: []error{ErrApplicationStandardRuntimeStale}, want: ErrApplicationStandardRuntimeStale, attempts: 1},
		{name: "other lock refusal", failures: []error{&pgconn.PgError{Code: "55P03"}}, wantPg: true, attempts: 1},
		{name: "busy past budget", failures: busyAttempts(api.ApplicationStandardRuntimeLockAttempts + 1), want: ErrApplicationStandardRuntimeBusy, attempts: api.ApplicationStandardRuntimeLockAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			got, err := retryRuntimeAdmissionBusy(t.Context(), func() (int, error) {
				attempts++
				if attempts <= len(tc.failures) {
					return 0, tc.failures[attempts-1]
				}
				return attempts, nil
			})
			if attempts != tc.attempts {
				t.Fatalf("attempts = %d, want %d", attempts, tc.attempts)
			}
			var pgErr *pgconn.PgError
			switch {
			case tc.wantPg:
				if !errors.As(err, &pgErr) {
					t.Fatalf("err = %v, want a PostgreSQL refusal", err)
				}
			case tc.want == nil:
				if err != nil || got != attempts {
					t.Fatalf("result = %d, %v; want commit", got, err)
				}
			default:
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			}
		})
	}
}

func TestRetryRuntimeAdmissionBusyStopsWithCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	_, err := retryRuntimeAdmissionBusy(ctx, func() (int, error) {
		attempts++
		cancel()
		return 0, ErrApplicationStandardRuntimeBusy
	})
	if attempts != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("canceled replay: attempts=%d err=%v", attempts, err)
	}
}

func TestRuntimePublicationOutcomeUnknown(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{err: nil},
		{err: fmt.Errorf("publish: %w", ErrConflict)},
		{err: ErrApplicationStandardRuntimeStale},
		{err: ErrApplicationStandardRuntimeBusy},
		{err: &pgconn.PgError{Code: "23514"}},
		{err: context.DeadlineExceeded, want: true},
		{err: errors.New("conn closed"), want: true},
	} {
		if got := RuntimePublicationOutcomeUnknown(tc.err); got != tc.want {
			t.Errorf("RuntimePublicationOutcomeUnknown(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func busyAttempts(n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = ErrApplicationStandardRuntimeBusy
	}
	return out
}

// runtimeAdmissionBusyBudget is the longest retryRuntimeAdmissionBusy sleeps
// before reporting busy. Every attempt rolls back before it sleeps, so a
// fail-fast assertion may allow this budget without permitting a lock wait.
func runtimeAdmissionBusyBudget() time.Duration {
	var total time.Duration
	for n := 1; n < api.ApplicationStandardRuntimeLockAttempts; n++ {
		total += min(api.ApplicationStandardRuntimeLockRetry<<(n-1), api.ApplicationStandardRuntimeLockMaxBackoff) + api.ApplicationStandardRuntimeLockRetry
	}
	return total
}
