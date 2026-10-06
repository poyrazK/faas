package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeCutoverStore = (*PgStore)(nil)

func (s *PgStore) CutoverDeploymentRuntimeUpgrade(ctx context.Context, r RuntimeUpgradeCutoverRequest) (RuntimeUpgradeCutover, error) {
	if err := r.validate(); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeUpgradeCutover{}, fmt.Errorf("begin runtime upgrade cutover: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRuntimeUpgradeCutoverDB(ctx, tx, r); err != nil {
		return RuntimeUpgradeCutover{}, fmt.Errorf("lock runtime upgrade cutover: %w", runtimeUpgradeBaselineError(err))
	}
	c, err := runtimeUpgradeCutoverDB(ctx, tx, r)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			err = runtimeUpgradeBaselineError(err)
		}
		return RuntimeUpgradeCutover{}, fmt.Errorf("apply runtime upgrade cutover: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeCutover{}, fmt.Errorf("commit runtime upgrade cutover: %w", runtimeUpgradeBaselineError(err))
	}
	return c, nil
}

func lockRuntimeUpgradeCutoverDB(ctx context.Context, tx pgx.Tx, r RuntimeUpgradeCutoverRequest) error {
	q := sqlc.New()
	owner, err := q.ReadRuntimeUpgradeCutoverOwner(ctx, tx, mustPgUUID(r.DeploymentID))
	if err != nil || owner.AppID != r.AppID || owner.AccountID != r.AccountID {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return ErrConflict
	}
	// Resolve the original owner before taking locks, then reread after acquiring
	// them. Read-committed observes child edits committed while we waited;
	// parent locks keep the final reads stable. Child rows remain unlocked
	// so their BEFORE publication fences cannot deadlock behind this reader.
	values, err := runtimeAppValuesDB(ctx, tx, r.AccountID, r.AppID, r.DeploymentID)
	if err != nil {
		return err
	}
	if values.EnvironmentID != "" {
		if _, err := q.LockRuntimeSecretEnvironment(ctx, tx, sqlc.LockRuntimeSecretEnvironmentParams{
			EnvironmentID: mustPgUUID(values.EnvironmentID), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), Scope: values.Scope,
		}); err != nil {
			return err
		}
	}
	if _, err := q.LockRuntimeUpgradeTargetApp(ctx, tx, mustPgUUID(r.DeploymentID)); err != nil {
		return err
	}
	if _, err := q.LockRuntimeUpgradeCutoverDeployments(ctx, tx, mustPgUUID(r.AppID)); err != nil {
		return err
	}
	for _, id := range []string{r.DeploymentID, r.ExpectedServingID} {
		if _, err := q.LockRuntimeConfigWorkloadSpec(ctx, tx, mustPgUUID(id)); err != nil {
			return err
		}
	}
	currentOwner, err := q.ReadRuntimeUpgradeCutoverOwner(ctx, tx, mustPgUUID(r.DeploymentID))
	if err != nil {
		return runtimeSecretFenceError(err)
	}
	if currentOwner != owner {
		return ErrConflict
	}
	current, err := runtimeAppValuesDB(ctx, tx, r.AccountID, r.AppID, r.DeploymentID)
	if err != nil {
		return err
	}
	if current.EnvironmentID != values.EnvironmentID || current.Scope != values.Scope {
		return ErrConflict
	}
	return nil
}

func runtimeUpgradeCutoverDB(ctx context.Context, tx pgx.Tx, r RuntimeUpgradeCutoverRequest) (RuntimeUpgradeCutover, error) {
	q, id := sqlc.New(), mustPgUUID(r.DeploymentID)
	old, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, id)
	if err == nil {
		c := runtimeUpgradeCutoverFromRow(old)
		if !r.matches(c) {
			return RuntimeUpgradeCutover{}, ErrConflict
		}
		return c, nil // acknowledge history, including after rollback/revocation
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeCutover{}, err
	}
	a, baseline, candidate, err := validateRuntimeUpgradeAcceptanceDB(ctx, tx, r.DeploymentID, true)
	if err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	if candidate.Status != DeployLive || candidate.EnvironmentWorkloadHeld() {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	if err := requireDeploymentLayerArtifactsTx(ctx, tx, r.DeploymentID); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	// No blocking fence acquisition follows this final freshness check.
	now := time.Now().UTC()
	if !validRuntimeUpgradeAcceptanceTime(a.StartedAt, a.ReadyAt, now) {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	c, err := newRuntimeUpgradeCutover(r, a, baseline, now)
	if err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	if _, err := q.InsertDeploymentRuntimeUpgradeCutover(ctx, tx, sqlc.InsertDeploymentRuntimeUpgradeCutoverParams{
		DeploymentID: id, ServingDeploymentID: mustPgUUID(c.ServingDeploymentID), TargetReleaseID: c.TargetReleaseID,
		WakeID: mustPgUUID(c.WakeID), QualificationReportSha256: c.QualificationReportSHA256,
		CutoverAt: pgtype.Timestamptz{Time: c.CutoverAt, Valid: true},
	}); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	count, err := q.ApplyDeploymentRuntimeUpgradeCutover(ctx, tx, sqlc.ApplyDeploymentRuntimeUpgradeCutoverParams{
		DeploymentID: id, ServingDeploymentID: mustPgUUID(c.ServingDeploymentID),
	})
	if err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	if count != 2 {
		return RuntimeUpgradeCutover{}, ErrConflict
	}
	if err := q.NotifyRuntimeUpgradeCutover(ctx, tx, sqlc.NotifyRuntimeUpgradeCutoverParams{AppID: r.AppID, DeploymentID: r.DeploymentID}); err != nil {
		return RuntimeUpgradeCutover{}, err
	}
	return c, nil
}

func (s *PgStore) DeploymentRuntimeUpgradeCutover(ctx context.Context, id string) (RuntimeUpgradeCutover, error) {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return RuntimeUpgradeCutover{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetDeploymentRuntimeUpgradeCutover(ctx, s.pool, mustPgUUID(id))
	return runtimeUpgradeCutoverFromRow(row), mapErr(err)
}

func runtimeUpgradeCutoverFromRow(c sqlc.DeploymentRuntimeUpgradeCutover) RuntimeUpgradeCutover {
	return RuntimeUpgradeCutover{DeploymentID: pgUUIDString(c.DeploymentID), ServingDeploymentID: pgUUIDString(c.ServingDeploymentID),
		TargetReleaseID: c.TargetReleaseID, WakeID: pgUUIDString(c.WakeID), QualificationReportSHA256: c.QualificationReportSha256, CutoverAt: c.CutoverAt.Time.UTC()}
}
