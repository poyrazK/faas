package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func writeBuildDependencyGateTx(ctx context.Context, tx pgx.Tx, id string, gate *BuildDependencyGate) error {
	if gate == nil {
		return nil
	}
	raw, err := json.Marshal(gate)
	if err != nil {
		return err
	}
	after := gate.CheckAfter
	if after.IsZero() {
		after = time.Unix(0, 0)
	}
	if err := sqlc.New().WriteBuildDependencyGate(ctx, tx, sqlc.WriteBuildDependencyGateParams{DeploymentID: mustPgUUID(id), Progress: raw, CheckAfter: pgtype.Timestamptz{Time: after, Valid: true}}); err != nil {
		return mapErr(err)
	}
	return sqlc.New().ProjectBuildDependencyGate(ctx, tx, sqlc.ProjectBuildDependencyGateParams{ID: mustPgUUID(id), Column2: raw})
}

func captureBuildDependencyGateTx(ctx context.Context, tx pgx.Tx, candidate Deployment, pins []DeploymentDependencyPin) error {
	gate, err := newBuildDependencyGate(candidate, pins, time.Now())
	if err != nil {
		return err
	}
	return writeBuildDependencyGateTx(ctx, tx, candidate.ID, gate)
}

func retryBuildDependencyGateTx(ctx context.Context, tx pgx.Tx, candidate Deployment, source string) error {
	raw, err := sqlc.New().ReadBuildDependencyGate(ctx, tx, mustPgUUID(source))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var gate BuildDependencyGate
	if err := json.Unmarshal(raw, &gate); err != nil {
		return err
	}
	return captureBuildDependencyGateTx(ctx, tx, candidate, gate.Dependencies)
}

func (s *PgStore) CheckBuildDependencies(ctx context.Context, claim Build, now time.Time) (*BuildDependencyGate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match cancellation's deployment -> build lock order. A pending check and
	// requeue commit together; stale workers cannot revive a cancelled claim.
	locked, err := sqlc.New().LockDeploymentHostingVerification(ctx, tx, mustPgUUID(claim.DeploymentID))
	if err != nil {
		return nil, mapErr(err)
	}
	if DeploymentStatus(locked.Status).IsTerminal() {
		return nil, ErrConflict
	}
	started, err := sqlc.New().ReadBuildDependencyClaim(ctx, tx, sqlc.ReadBuildDependencyClaimParams{ID: mustPgUUID(claim.ID), DeploymentID: mustPgUUID(claim.DeploymentID)})
	if err != nil {
		return nil, mapErr(err)
	}
	if !started.Valid || !started.Time.Equal(claim.StartedAt) {
		return nil, ErrConflict
	}
	raw, err := sqlc.New().ReadBuildDependencyGate(ctx, tx, mustPgUUID(claim.DeploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var gate BuildDependencyGate
	if err := json.Unmarshal(raw, &gate); err != nil {
		return nil, err
	}
	updated, err := evaluateBuildDependencies(gate, now, func(pin DeploymentDependencyPin) (Deployment, bool, error) {
		target, err := sqlc.New().ReadBuildDependencyTarget(ctx, tx, sqlc.ReadBuildDependencyTargetParams{CandidateID: mustPgUUID(claim.DeploymentID), DependencyID: mustPgUUID(pin.DeploymentID), DependencyAppID: mustPgUUID(pin.AppID), Service: pin.Service})
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, false, nil
		}
		if err != nil {
			return Deployment{}, false, err
		}
		return Deployment{ID: pin.DeploymentID, Status: DeploymentStatus(target.Status), TrafficPercent: int(target.TrafficPercent), ParkedReason: target.ParkedReason}, target.Latest.Valid && target.Latest.Bool, nil
	})
	if err != nil {
		return nil, err
	}
	if err := writeBuildDependencyGateTx(ctx, tx, claim.DeploymentID, &updated); err != nil {
		return nil, err
	}
	if updated.Status == "waiting" {
		if err := sqlc.New().DeferBuildDependencyClaim(ctx, tx, mustPgUUID(claim.ID)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &updated, nil
}

var _ BuildDependencyGateStore = (*PgStore)(nil)
var _ BuildDependencyGateStore = (*MemStore)(nil)
