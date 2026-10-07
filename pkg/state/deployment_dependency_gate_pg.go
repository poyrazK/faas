package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func captureDeploymentDependenciesTx(ctx context.Context, tx pgx.Tx, candidate Deployment) error {
	owner, err := sqlc.New().ReadProjectDependencyOwner(ctx, tx, mustPgUUID(candidate.AppID))
	if err != nil {
		return mapErr(err)
	}
	var manifest AppManifest
	if err := json.Unmarshal(owner.Manifest, &manifest); err != nil {
		return fmt.Errorf("state: decode dependency owner manifest: %w", err)
	}
	names, err := healthyProjectDependencies(manifest)
	if err != nil || len(names) == 0 {
		return err
	}
	if owner.ProjectID == "" || candidate.EnvironmentWorkloadHeld() {
		return fmt.Errorf("state: healthy dependency gates require an ordinary project deployment: %w", ErrInvalidArgument)
	}
	targets, err := sqlc.New().CaptureProjectDependencyTargets(ctx, tx, sqlc.CaptureProjectDependencyTargetsParams{
		Scope: normalizedDeploymentScope(candidate.Scope), AccountID: mustPgUUID(owner.AccountID), ProjectID: mustPgUUID(owner.ProjectID),
		Services: names, PreviewPrNumber: owner.PreviewPrNumber, IsPreview: owner.PreviewOfSlug != "",
	})
	if err != nil {
		return fmt.Errorf("state: capture dependency targets: %w", err)
	}
	pins := make([]DeploymentDependencyPin, 0, len(names))
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if seen[target.Service] || target.AppID == candidate.AppID {
			return fmt.Errorf("state: ambiguous or self dependency %q: %w", target.Service, ErrInvalidArgument)
		}
		if target.DeploymentID == "" {
			return fmt.Errorf("state: dependency %q has no accepted deployment in this environment: %w", target.Service, ErrInvalidArgument)
		}
		seen[target.Service] = true
		pins = append(pins, DeploymentDependencyPin{Service: target.Service, AppID: target.AppID, DeploymentID: target.DeploymentID})
	}
	for _, name := range names {
		if !seen[name] {
			return fmt.Errorf("state: dependency %q has no project member in this environment: %w", name, ErrInvalidArgument)
		}
	}
	encoded, err := json.Marshal(pins)
	if err != nil {
		return err
	}
	return sqlc.New().InsertDeploymentDependencyGate(ctx, tx, sqlc.InsertDeploymentDependencyGateParams{
		DeploymentID: mustPgUUID(candidate.ID), Pins: encoded,
	})
}

func (s *PgStore) CheckDeploymentDependencies(ctx context.Context, id string, now time.Time) (*DeploymentDependencyGate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	dep, err := deploymentByIDDB(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	// The gate and its stage projection use the same deployment-first lock
	// order as promotion, retry, and concurrent stage transitions.
	locked, err := sqlc.New().LockDeploymentHostingVerification(ctx, tx, mustPgUUID(id))
	if err != nil {
		return nil, mapErr(err)
	}
	dep.Status = DeploymentStatus(locked.Status)
	gate, gateErr := checkDeploymentDependenciesTx(ctx, tx, dep, now, true)
	if gateErr != nil {
		var blocker *DependencyGateError
		if !errors.As(gateErr, &blocker) {
			return nil, gateErr
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return gate, gateErr
}

func checkDeploymentDependenciesTx(ctx context.Context, tx pgx.Tx, candidate Deployment, now time.Time, persist bool) (*DeploymentDependencyGate, error) {
	row, err := sqlc.New().ReadDeploymentDependencyGate(ctx, tx, mustPgUUID(candidate.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.PreviouslyServed {
		return nil, nil
	}
	gate := DeploymentDependencyGate{Status: row.Status, Blocker: row.Blocker}
	if err := json.Unmarshal(row.Pins, &gate.Dependencies); err != nil {
		return nil, fmt.Errorf("state: decode dependency pins: %w", err)
	}
	if row.StartedAt.Valid {
		gate.StartedAt, gate.DeadlineAt = &row.StartedAt.Time, &row.DeadlineAt.Time
	}
	updated, gateErr := evaluateDependencyGate(gate, now, func(pin DeploymentDependencyPin) (Deployment, error) {
		target, err := sqlc.New().ReadDependencyGateTarget(ctx, tx, sqlc.ReadDependencyGateTargetParams{
			CandidateID: mustPgUUID(candidate.ID), DependencyID: mustPgUUID(pin.DeploymentID), DependencyAppID: mustPgUUID(pin.AppID),
		})
		if err != nil {
			return Deployment{}, mapErr(err)
		}
		return Deployment{Status: DeploymentStatus(target.Status), TrafficPercent: int(target.TrafficPercent), ParkedReason: target.ParkedReason}, nil
	})
	if persist {
		if err := sqlc.New().UpdateDeploymentDependencyGate(ctx, tx, sqlc.UpdateDeploymentDependencyGateParams{
			DeploymentID: mustPgUUID(candidate.ID), Status: updated.Status, Blocker: updated.Blocker,
			StartedAt: pgtype.Timestamptz{Time: *updated.StartedAt, Valid: true}, DeadlineAt: pgtype.Timestamptz{Time: *updated.DeadlineAt, Valid: true},
		}); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(updated)
		if err != nil {
			return nil, err
		}
		if err := sqlc.New().ProjectDeploymentDependencyGateProgress(ctx, tx, sqlc.ProjectDeploymentDependencyGateProgressParams{
			DeploymentID: mustPgUUID(candidate.ID), Progress: encoded,
		}); err != nil {
			return nil, err
		}
	}
	return &updated, gateErr
}

var _ DeploymentDependencyGateStore = (*MemStore)(nil)
var _ DeploymentDependencyGateStore = (*PgStore)(nil)
