package state

// adr: 431

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentArtifactScanEvidenceStore = (*PgStore)(nil)

// Acquire the owner/control/artifact fences before reading scan selections.
// An empty publisher acquires no approval: each selected proof is separately
// cryptographically reverified by lockArtifactScanParent under these fences.
func lockArtifactEvidenceOwner(ctx context.Context, tx pgx.Tx, accountID, appID, depID, workload string) (sqlc.GetDeploymentArtifactWorkloadsRow, error) {
	q := sqlc.New()
	params := sqlc.GetDeploymentArtifactWorkloadsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)}
	if _, err := q.GetDeploymentArtifactWorkloads(ctx, tx, params); err != nil {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, registryVerificationError(err)
	}
	if _, err := q.LockDeploymentRegistryVerification(ctx, tx, sqlc.LockDeploymentRegistryVerificationParams{AccountID: params.AccountID, AppID: params.AppID, DeploymentID: params.DeploymentID, WorkloadName: workload}); err != nil {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, registryVerificationError(err)
	}
	return q.GetDeploymentArtifactWorkloads(ctx, tx, params)
}

func freshArtifactScanLocked(ctx context.Context, tx pgx.Tx, accountID, appID, depID, workload string) (DeploymentArtifactScan, artifactScanParents, error) {
	row, err := sqlc.New().GetCurrentDeploymentArtifactScan(ctx, tx, sqlc.GetCurrentDeploymentArtifactScanParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: workload})
	if err != nil {
		return DeploymentArtifactScan{}, artifactScanParents{}, registryVerificationError(err)
	}
	value, err := artifactScanRow(row)
	if err != nil {
		return DeploymentArtifactScan{}, artifactScanParents{}, err
	}
	parents, err := lockArtifactScanParent(ctx, tx, value.Input)
	if errors.Is(err, ErrApplicationStandardReviewBusy) {
		err = ErrApplicationStandardRuntimeBusy
	}
	return value, parents, err
}

func (s *PgStore) GetFreshDeploymentArtifactScan(ctx context.Context, accountID, appID, depID, workload string) (DeploymentArtifactScan, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, workload) {
		return DeploymentArtifactScan{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockArtifactEvidenceOwner(ctx, tx, accountID, appID, depID, workload); err != nil {
		return DeploymentArtifactScan{}, err
	}
	value, parents, err := freshArtifactScanLocked(ctx, tx, accountID, appID, depID, workload)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	if !now.Valid {
		return DeploymentArtifactScan{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkDeploymentArtifactScanLease(value, parents, now.Time); err != nil {
		return DeploymentArtifactScan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentArtifactScan{}, err
	}
	return value, nil
}

func freshArtifactBaseScanLocked(ctx context.Context, tx pgx.Tx, parents artifactScanParents) (BaseImageScan, error) {
	root := parents.Rootfs.Input
	row, err := sqlc.New().GetFreshBaseImageScan(ctx, tx, sqlc.GetFreshBaseImageScanParams{ProducerID: mustPgUUID(root.BaseProducerID), ProducerHash: root.BaseInputHash, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return BaseImageScan{}, err
	}
	return baseImageScanRow(row)
}

func (s *PgStore) GetFreshDeploymentArtifactScanEvidence(ctx context.Context, accountID, appID, depID string) (DeploymentArtifactScanEvidence, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentArtifactScanEvidence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	defer tx.Rollback(ctx)
	owner, err := readArtifactEvidenceOwner(ctx, tx, accountID, appID, depID)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	value, parents, err := readArtifactEvidenceComponents(ctx, tx, accountID, appID, depID, owner.Sidecars)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	if err := finishArtifactEvidenceTransaction(ctx, tx, &value, parents); err != nil {
		return DeploymentArtifactScanEvidence{}, err
	}
	return value, tx.Commit(ctx)
}

func readArtifactEvidenceOwner(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (sqlc.GetDeploymentArtifactWorkloadsRow, error) {
	preflight, err := sqlc.New().GetDeploymentArtifactWorkloads(ctx, tx, sqlc.GetDeploymentArtifactWorkloadsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)})
	if err != nil {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, registryVerificationError(err)
	}
	if !preflight.HasRegistryProducers {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, ErrDeploymentArtifactScanEvidenceAbsent
	}
	owner, err := lockArtifactEvidenceOwner(ctx, tx, accountID, appID, depID, "")
	if err != nil {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, err
	}
	if !owner.HasRegistryProducers {
		return sqlc.GetDeploymentArtifactWorkloadsRow{}, ErrDeploymentArtifactScanEvidenceAbsent
	}
	return owner, nil
}

func readArtifactEvidenceComponents(ctx context.Context, tx pgx.Tx, accountID, appID, depID string, sidecars []byte) (DeploymentArtifactScanEvidence, []artifactScanParents, error) {
	names, err := artifactScanWorkloads(sidecars)
	if err != nil {
		return DeploymentArtifactScanEvidence{}, nil, err
	}
	value := DeploymentArtifactScanEvidence{Components: []DeploymentArtifactScan{}, Bases: []BaseImageScan{}}
	parents := make([]artifactScanParents, 0, len(names))
	bases := map[string]bool{}
	for _, name := range names {
		scan, p, err := freshArtifactScanLocked(ctx, tx, accountID, appID, depID, name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				err = ErrApplicationStandardRuntimeStale
			}
			return DeploymentArtifactScanEvidence{}, nil, err
		}
		value.Components, parents = append(value.Components, scan), append(parents, p)
		value.Artifacts = append(value.Artifacts, runtimeArtifactFromRootfs(p.Rootfs))
		if id := p.Rootfs.Input.BaseProducerID; id != "" && !bases[id] {
			base, err := freshArtifactBaseScanLocked(ctx, tx, p)
			if err != nil {
				return DeploymentArtifactScanEvidence{}, nil, err
			}
			value.Bases, bases[id] = append(value.Bases, base), true
			value.Artifacts = append(value.Artifacts, runtimeArtifactFromBaseScan(base))
		}
	}
	return value, parents, nil
}

func finishArtifactEvidenceTransaction(ctx context.Context, tx pgx.Tx, value *DeploymentArtifactScanEvidence, parents []artifactScanParents) error {
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !now.Valid {
		if err == nil {
			err = ErrApplicationStandardRuntimeStale
		}
		return err
	}
	for i, scan := range value.Components {
		if err := checkDeploymentArtifactScanLease(scan, parents[i], now.Time); err != nil {
			return err
		}
	}
	if err := finishArtifactScanEvidence(value, now.Time); err != nil {
		return err
	}
	return nil // Caller owns the transaction; native admission retains these fences.
}
