package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type snapshotPrimeError struct {
	cause   error
	attempt snapshotPrimeAttempt
}

func (e *snapshotPrimeError) Error() string { return e.cause.Error() }
func (e *snapshotPrimeError) Unwrap() error { return e.cause }

// SnapshotPrimeAttemptError retains execution ownership through the caller's
// error path without changing typed startup errors or retry classification.
func SnapshotPrimeAttemptError(ctx context.Context, cause error) error {
	attempt, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	if cause == nil || !ok {
		return cause
	}
	return &snapshotPrimeError{cause: cause, attempt: attempt}
}

type snapshotPrimeFailureKey struct{}
type snapshotPrimeFailureGuard struct{ deployment Deployment }

// WithSnapshotPrimeFailure binds the failure write to the row inspected by
// the scheduler. A watchdog may already have finalized this same attempt;
// its history preserves the stage clock needed for refining its diagnostic.
func WithSnapshotPrimeFailure(ctx context.Context, cause error, current Deployment) (context.Context, error) {
	var prime *snapshotPrimeError
	if errors.As(cause, &prime) {
		if err := matchSnapshotPrimeAttempt(prime.attempt, current.AppID, current.ID, current); err != nil {
			if !sameFailedPrimeAttempt(prime.attempt, current) {
				return ctx, ErrSnapshotPrimeStale
			}
		}
	}
	current.StageState = bytes.Clone(current.StageState)
	return context.WithValue(ctx, snapshotPrimeFailureKey{}, snapshotPrimeFailureGuard{current}), nil
}

func sameFailedPrimeAttempt(expected snapshotPrimeAttempt, current Deployment) bool {
	if current.Status != DeployFailed || expected.AppID != current.AppID || expected.DeploymentID != current.ID ||
		expected.RootfsKey != current.RootfsKey || expected.RootfsPath != current.RootfsPath || expected.StartedAt.IsZero() {
		return false
	}
	var stages StageState
	if json.Unmarshal(current.StageState, &stages) != nil || len(stages.History) == 0 {
		return false
	}
	last := stages.History[len(stages.History)-1]
	return last.Name == expected.Stage && last.StartedAt != nil && last.StartedAt.Equal(expected.StartedAt)
}

func checkSnapshotPrimeFailure(ctx context.Context, current Deployment) error {
	guard, ok := ctx.Value(snapshotPrimeFailureKey{}).(snapshotPrimeFailureGuard)
	if !ok {
		return nil
	}
	d := guard.deployment
	if d.ID != current.ID || d.AppID != current.AppID || d.Status != current.Status || d.RootfsKey != current.RootfsKey ||
		d.RootfsPath != current.RootfsPath || d.Error != current.Error || d.ErrorCode != current.ErrorCode || !bytes.Equal(d.StageState, current.StageState) {
		return ErrSnapshotPrimeStale
	}
	return nil
}

func checkSnapshotPrimeFailureTx(ctx context.Context, tx pgx.Tx, id string) error {
	if _, ok := ctx.Value(snapshotPrimeFailureKey{}).(snapshotPrimeFailureGuard); !ok {
		return nil
	}
	row, err := sqlc.New().LockSnapshotPrimeFailure(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSnapshotPrimeStale
	}
	if err != nil {
		return err
	}
	return checkSnapshotPrimeFailure(ctx, Deployment{ID: row.DeploymentID, AppID: row.AppID, Status: DeploymentStatus(row.Status), RootfsKey: row.RootfsKey, RootfsPath: row.RootfsPath, StageState: row.StageState, Error: row.Error, ErrorCode: row.ErrorCode})
}
