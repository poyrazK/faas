// adr: 595 — replay instance transactions refused by a busy runtime fence.
package state

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

// runtimeAdmissionBusy reports the instance guard's non-waiting fence refusal.
// The guard raises before any write survives, so the whole transaction rolled
// back and replaying it cannot duplicate a row or a reservation.
func runtimeAdmissionBusy(err error) bool {
	if errors.Is(err, ErrApplicationStandardRuntimeBusy) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "application_standard_runtime_busy"
}

// retryRuntimeAdmissionBusy replays one complete transaction while the runtime
// fences report busy. Every wake, publication, native grant and state
// transition of an app's instances takes those fences without waiting, so
// concurrent wakes of one app or deployment routinely collide for a few
// milliseconds. attempt must open and finish its own transaction.
func retryRuntimeAdmissionBusy[T any](ctx context.Context, attempt func() (T, error)) (T, error) {
	for n := 1; ; n++ {
		out, err := attempt()
		if !runtimeAdmissionBusy(err) || n >= api.ApplicationStandardRuntimeLockAttempts {
			return out, err
		}
		retry := api.ApplicationStandardRuntimeLockRetry
		backoff := min(retry<<(n-1), api.ApplicationStandardRuntimeLockMaxBackoff)
		timer := time.NewTimer(backoff + rand.N(retry))
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}
