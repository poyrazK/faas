package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAppForkLeaseLost means a lease-guarded fork update matched no row: the
// lease token is stale, another scheduler took the fork over, or the fork is
// no longer in the expected status. The caller must stop acting on the fork.
var ErrAppForkLeaseLost = errors.New("state: app fork lease lost")

// FinishAppForkParams is schedd's terminal compare-and-swap for a fork it
// holds. Status is expired, cancelled or failed; a failed fork needs a code
// and message, other statuses must carry neither.
type FinishAppForkParams struct {
	ForkID         string
	LeaseToken     string
	Status         AppForkStatus
	FailureCode    string
	FailureMessage string
	FinishedAt     time.Time
}

func (p FinishAppForkParams) validate() error {
	switch {
	case p.ForkID == "" || p.LeaseToken == "" || p.FinishedAt.IsZero():
		return fmt.Errorf("%w: fork, lease and finish time are required", ErrAppForkInvalid)
	case !p.Status.Terminal():
		return fmt.Errorf("%w: %q is not a terminal fork status", ErrAppForkInvalid, p.Status)
	case (p.Status == AppForkFailed) != (strings.TrimSpace(p.FailureCode) != ""):
		return fmt.Errorf("%w: only a failed fork carries a failure code", ErrAppForkInvalid)
	case (p.FailureCode == "") != (p.FailureMessage == ""):
		return fmt.Errorf("%w: failure code and message go together", ErrAppForkInvalid)
	case len(p.FailureCode) > 64 || len(p.FailureMessage) > 4096:
		return fmt.Errorf("%w: failure code or message too long", ErrAppForkInvalid)
	}
	return nil
}

// AppForkLifecycleStore is schedd's side of ADR-732. Every restoring or
// running fork is held under a lease; every method that changes such a fork
// is guarded by its lease token and returns ErrAppForkLeaseLost when the
// token no longer matches.
type AppForkLifecycleStore interface {
	// ClaimNextAppFork leases the oldest queued, uncancelled, unexpired
	// fork and moves it to restoring. ErrNotFound when there is none.
	ClaimNextAppFork(ctx context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error)
	RenewAppForkLease(ctx context.Context, forkID, leaseToken string, now time.Time, lease time.Duration) (AppFork, error)
	// MarkAppForkRunning records the restored instance and its snapshot.
	MarkAppForkRunning(ctx context.Context, forkID, leaseToken, snapshotID, instanceID string, now time.Time) (AppFork, error)
	FinishAppFork(ctx context.Context, params FinishAppForkParams) (AppFork, error)
	// ExpireUnclaimedAppForks ends queued forks that expired or were
	// cancelled before any scheduler claimed them.
	ExpireUnclaimedAppForks(ctx context.Context, now time.Time) ([]AppFork, error)
	// AppForksDueForTeardown lists forks owner holds that reached their TTL
	// or were cancelled, soonest expiry first.
	AppForksDueForTeardown(ctx context.Context, owner string, now time.Time, limit int) ([]AppFork, error)
	// TakeOverAbandonedAppFork leases one restoring or running fork whose
	// lease expired. ErrNotFound when there is none.
	TakeOverAbandonedAppFork(ctx context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error)
}

func validLease(owner string, now time.Time, lease time.Duration) error {
	if strings.TrimSpace(owner) == "" || len(owner) > 256 || now.IsZero() || lease <= 0 {
		return fmt.Errorf("%w: owner, time and a positive lease are required", ErrAppForkInvalid)
	}
	return nil
}
