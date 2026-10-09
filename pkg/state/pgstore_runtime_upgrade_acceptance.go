package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeAcceptanceStore = (*PgStore)(nil)

// Called only after runtimeAppConfigFenceDB has locked the original
// environment/app/candidate. Retain the serving artifact and qualification
// row locks through publication so drift/revocation cannot race a receipt.
func prepareRuntimeUpgradeAcceptanceDB(ctx context.Context, db sqlc.DBTX, p RuntimeInstancePublication) (*RuntimeUpgradeAcceptance, error) {
	if p.RuntimeUpgradeColdBoot == nil {
		return nil, nil
	}
	q, id := sqlc.New(), mustPgUUID(p.Fence.DeploymentID)
	row, err := q.GetDeploymentRuntimeUpgradeBaseline(ctx, db, id)
	if err != nil {
		return nil, runtimeSecretFenceError(err)
	}
	if _, err := q.LockRuntimeUpgradeAcceptanceServing(ctx, db, id); err != nil {
		return nil, runtimeSecretFenceError(err)
	}
	baseline := runtimeUpgradeBaselineFromRow(row)
	if _, err := q.LockRuntimeConfigWorkloadSpec(ctx, db, row.ServingDeploymentID); err != nil {
		return nil, runtimeSecretFenceError(err)
	}
	current, err := runtimeUpgradeBaselineDB(ctx, db, p.Fence.DeploymentID, baseline.ServingDeploymentID)
	if err != nil {
		return nil, err
	}
	if !sameRuntimeUpgradeBaseline(baseline, current) {
		return nil, ErrConflict
	}
	candidate, target, qualification, err := runtimeUpgradeAcceptanceInputsDB(ctx, db, p.Fence.DeploymentID, baseline, true)
	if err != nil {
		return nil, err
	}
	acceptance, err := newRuntimeUpgradeAcceptance(p, candidate, target, qualification, time.Now().UTC())
	return &acceptance, err
}

func insertRuntimeUpgradeAcceptanceDB(ctx context.Context, db sqlc.DBTX, a RuntimeUpgradeAcceptance) error {
	_, err := sqlc.New().InsertDeploymentRuntimeUpgradeAcceptance(ctx, db, sqlc.InsertDeploymentRuntimeUpgradeAcceptanceParams{
		DeploymentID: mustPgUUID(a.DeploymentID), TargetReleaseID: a.TargetReleaseID, RootfsKey: a.RootfsKey,
		InstanceID: mustPgUUID(a.InstanceID), NodeID: mustPgUUID(a.NodeID), WakeID: mustPgUUID(a.WakeID), Profile: a.Profile,
		ConfigurationFingerprint: a.ConfigurationFingerprint, SecretFingerprint: a.SecretFingerprint,
		QualificationReportSha256: a.QualificationReportSHA256,
		StartedAt:                 pgtype.Timestamptz{Time: a.StartedAt, Valid: true}, ReadyAt: pgtype.Timestamptz{Time: a.ReadyAt, Valid: true},
	})
	return runtimeSecretFenceError(err)
}

func (s *PgStore) DeploymentRuntimeUpgradeAcceptance(ctx context.Context, id string) (RuntimeUpgradeAcceptance, error) {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return RuntimeUpgradeAcceptance{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetDeploymentRuntimeUpgradeAcceptance(ctx, s.pool, mustPgUUID(id))
	return runtimeUpgradeAcceptanceFromRow(row), mapErr(err)
}

func (s *PgStore) ValidateDeploymentRuntimeUpgradeAcceptance(ctx context.Context, id string) error {
	if validateRuntimeAppEnvIDs(id, id, id) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, _, err := validateRuntimeUpgradeAcceptanceDB(ctx, tx, id, false); err != nil {
		return err
	}
	return runtimeUpgradeBaselineError(tx.Commit(ctx))
}

func validateRuntimeUpgradeAcceptanceDB(ctx context.Context, db sqlc.DBTX, id string, lockQualification bool) (RuntimeUpgradeAcceptance, RuntimeUpgradeBaseline, Deployment, error) {
	q := sqlc.New()
	a, err := q.GetDeploymentRuntimeUpgradeAcceptance(ctx, db, mustPgUUID(id))
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, mapErr(err) // only receipt absence returns not-found
	}
	row, err := q.GetDeploymentRuntimeUpgradeBaseline(ctx, db, mustPgUUID(id))
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, runtimeSecretFenceError(err)
	}
	baseline := runtimeUpgradeBaselineFromRow(row)
	current, err := runtimeUpgradeBaselineDB(ctx, db, id, baseline.ServingDeploymentID)
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, err
	}
	if !sameRuntimeUpgradeBaseline(baseline, current) {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, ErrConflict
	}
	candidate, target, qualification, err := runtimeUpgradeAcceptanceInputsDB(ctx, db, id, baseline, lockQualification)
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, err
	}
	app, err := q.AppByID(ctx, db, mustPgUUID(candidate.AppID))
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, runtimeSecretFenceError(err)
	}
	values, err := runtimeAppValuesDB(ctx, db, pgUUIDString(app.AccountID), candidate.AppID, id)
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, err
	}
	fence, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, err
	}
	if err := validateRuntimeUpgradeAcceptance(runtimeUpgradeAcceptanceFromRow(a), candidate, target, qualification, fence, time.Now().UTC()); err != nil {
		return RuntimeUpgradeAcceptance{}, RuntimeUpgradeBaseline{}, Deployment{}, err
	}
	return runtimeUpgradeAcceptanceFromRow(a), baseline, candidate, nil
}

func runtimeUpgradeAcceptanceInputsDB(ctx context.Context, db sqlc.DBTX, id string, baseline RuntimeUpgradeBaseline, lockQualification bool) (Deployment, RuntimeRelease, RuntimeReleaseQualification, error) {
	q := sqlc.New()
	rows, err := q.ReadRuntimeUpgradeBaselineDeployments(ctx, db, sqlc.ReadRuntimeUpgradeBaselineDeploymentsParams{
		DeploymentID: mustPgUUID(id), ServingDeploymentID: mustPgUUID(baseline.ServingDeploymentID),
	})
	if err != nil {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, err
	}
	var candidate Deployment
	for _, row := range rows {
		if row.ID == id {
			candidate, err = runtimeUpgradeDeploymentFromRow(row)
			if err != nil {
				return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, err
			}
		}
	}
	if candidate.ID == "" {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, ErrConflict
	}
	app, err := q.AppByID(ctx, db, mustPgUUID(candidate.AppID))
	if err != nil {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, runtimeSecretFenceError(err)
	}
	bound, err := q.GetArtifactRuntimeRelease(ctx, db, sqlc.GetArtifactRuntimeReleaseParams{
		AccountID: app.AccountID, RootfsKey: candidate.RootfsKey,
	})
	if err != nil {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, runtimeSecretFenceError(err)
	}
	target := runtimeReleaseFromRow(bound)
	if target.ID != baseline.TargetReleaseID {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, ErrConflict
	}
	var proof sqlc.RuntimeReleaseQualification
	if lockQualification {
		proof, err = q.LockRuntimeReleaseQualification(ctx, db, target.ID)
	} else {
		proof, err = q.GetRuntimeReleaseQualification(ctx, db, target.ID)
	}
	if err != nil {
		return Deployment{}, RuntimeRelease{}, RuntimeReleaseQualification{}, runtimeSecretFenceError(err)
	}
	return candidate, target, runtimeQualificationFromRow(proof), nil
}

func runtimeUpgradeAcceptanceFromRow(a sqlc.DeploymentRuntimeUpgradeAcceptance) RuntimeUpgradeAcceptance {
	return RuntimeUpgradeAcceptance{DeploymentID: pgUUIDString(a.DeploymentID), TargetReleaseID: a.TargetReleaseID, RootfsKey: a.RootfsKey,
		InstanceID: pgUUIDString(a.InstanceID), NodeID: pgUUIDString(a.NodeID), WakeID: pgUUIDString(a.WakeID), Profile: a.Profile,
		ConfigurationFingerprint: a.ConfigurationFingerprint, SecretFingerprint: a.SecretFingerprint,
		QualificationReportSHA256: a.QualificationReportSha256, StartedAt: a.StartedAt.Time, ReadyAt: a.ReadyAt.Time}
}
