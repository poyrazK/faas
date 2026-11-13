package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrSnapshotPrimeStale retires delayed work without failing a newer attempt
// that may now use the same deployment ID (historical rollback re-primes it).
var ErrSnapshotPrimeStale = fmt.Errorf("%w: snapshot prime attempt changed", ErrConflict)

type snapshotPrimeAttemptKey struct{}
type snapshotPrimeAttempt struct {
	AppID, DeploymentID, RootfsKey, RootfsPath string
	Status                                     DeploymentStatus
	Stage                                      StageName
	StartedAt                                  time.Time
}

func snapshotPrimeAttemptFor(d Deployment) (snapshotPrimeAttempt, error) {
	if d.Status.IsTerminal() {
		return snapshotPrimeAttempt{}, ErrSnapshotPrimeStale
	}
	var stages StageState
	if len(d.StageState) > 0 {
		if err := json.Unmarshal(d.StageState, &stages); err != nil {
			return snapshotPrimeAttempt{}, fmt.Errorf("read snapshot prime stage: %w", err)
		}
	}
	// Completed readiness is durable. A retained LIVE artifact does not
	// authorize another boot when its prime notification is replayed.
	if d.Status == DeployLive && (stages.Current == "" || stages.Current == StageReadiness) {
		return snapshotPrimeAttempt{}, ErrSnapshotPrimeStale
	}
	attempt := snapshotPrimeAttempt{AppID: d.AppID, DeploymentID: d.ID, RootfsKey: d.RootfsKey, RootfsPath: d.RootfsPath, Status: d.Status, Stage: stages.Current}
	if stages.CurrentStartedAt != nil {
		attempt.StartedAt = stages.CurrentStartedAt.UTC()
	}
	return attempt, nil
}

// WithSnapshotPrimeAttempt fences the active lifecycle attempt, not merely its
// retained artifact. Existing active legacy prime callers remain compatible;
// failed, cancelled and superseded rows never authorize a new prime.
func WithSnapshotPrimeAttempt(ctx context.Context, d Deployment) (context.Context, error) {
	attempt, err := snapshotPrimeAttemptFor(d)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, snapshotPrimeAttemptKey{}, attempt), nil
}

func matchSnapshotPrimeAttempt(expected snapshotPrimeAttempt, appID, deploymentID string, d Deployment) error {
	actual, err := snapshotPrimeAttemptFor(d)
	if err != nil {
		return err
	}
	if expected.AppID != appID || expected.DeploymentID != deploymentID || expected.AppID != actual.AppID ||
		expected.DeploymentID != actual.DeploymentID || expected.Status != actual.Status || expected.RootfsKey != actual.RootfsKey ||
		expected.RootfsPath != actual.RootfsPath || expected.Stage != actual.Stage || !expected.StartedAt.Equal(actual.StartedAt) {
		return ErrSnapshotPrimeStale
	}
	return nil
}

// CheckSnapshotPrimeAttempt is also used immediately before VM effects. The
// publication store repeats the check under its existing ownership locks.
func CheckSnapshotPrimeAttempt(ctx context.Context, store interface {
	DeploymentByID(context.Context, string) (Deployment, error)
}) error {
	expected, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	if !ok {
		return nil
	}
	d, err := store.DeploymentByID(ctx, expected.DeploymentID)
	if errors.Is(err, ErrNotFound) {
		return ErrSnapshotPrimeStale
	}
	if err != nil {
		return err
	}
	return matchSnapshotPrimeAttempt(expected, expected.AppID, expected.DeploymentID, d)
}

func (m *MemStore) checkSnapshotPrimeAttemptLocked(ctx context.Context, appID, deploymentID string) error {
	expected, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	if !ok {
		return nil
	}
	d, ok := m.deployments[deploymentID]
	if !ok {
		return ErrSnapshotPrimeStale
	}
	return matchSnapshotPrimeAttempt(expected, appID, deploymentID, d)
}
func checkSnapshotPrimeAttemptTx(ctx context.Context, tx pgx.Tx, appID, deploymentID string) error {
	expected, ok := ctx.Value(snapshotPrimeAttemptKey{}).(snapshotPrimeAttempt)
	if !ok {
		return nil
	}
	row, err := sqlc.New().LockSnapshotPrimeAttempt(ctx, tx, mustPgUUID(deploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSnapshotPrimeStale
	}
	if err != nil {
		return err
	}
	d := Deployment{ID: row.DeploymentID, AppID: row.AppID, Status: DeploymentStatus(row.Status), RootfsKey: row.RootfsKey, RootfsPath: row.RootfsPath, StageState: row.StageState}
	return matchSnapshotPrimeAttempt(expected, appID, deploymentID, d)
}
