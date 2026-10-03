package state

// adr: 435. Current producer fences and the database clock guard publication.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentRuntimeScanStore = (*PgStore)(nil)

func deploymentRuntimeScanRow(row sqlc.DeploymentRuntimeScan) (DeploymentRuntimeScan, error) {
	var in DeploymentRuntimeScanInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	in.ID = pgUUIDString(row.ID)
	if in.DeploymentID != pgUUIDString(row.DeploymentID) || !row.ScannedAt.Valid || !row.ExpiresAt.Valid {
		return DeploymentRuntimeScan{}, fmt.Errorf("runtime scan stored owner/clock mismatch")
	}
	value := DeploymentRuntimeScan{ID: in.ID, InputHash: row.InputHash, Input: in, ScannedAt: row.ScannedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if err := validateDeploymentRuntimeScan(value); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	return value, nil
}

func (s *PgStore) PublishDeploymentRuntimeScan(ctx context.Context, input DeploymentRuntimeScanInput) (DeploymentRuntimeScan, error) {
	in, hash, err := prepareDeploymentRuntimeScan(input)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	defer tx.Rollback(ctx)
	current, err := readRuntimeProducerInputsTx(ctx, tx, in.AccountID, in.AppID, in.DeploymentID)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	if current.InputHash != in.Facts.InputHash {
		return DeploymentRuntimeScan{}, ErrApplicationStandardRuntimeStale
	}
	value, exists, err := runtimeScanRetryTx(ctx, tx, in, hash)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	if exists {
		now, e := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
		if e != nil || !now.Valid || !current.ExpiresAt.After(now.Time) {
			return DeploymentRuntimeScan{}, errors.Join(ErrApplicationStandardRuntimeStale, e)
		}
	} else {
		value, err = insertRuntimeScanTx(ctx, tx, in, hash, current)
		if err != nil {
			return DeploymentRuntimeScan{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	return value, nil
}

func runtimeScanRetryTx(ctx context.Context, tx pgx.Tx, in DeploymentRuntimeScanInput, hash string) (DeploymentRuntimeScan, bool, error) {
	q := sqlc.New()
	row, err := q.GetDeploymentRuntimeScanByID(ctx, tx, mustPgUUID(in.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRuntimeScan{}, false, nil
	}
	if err != nil {
		return DeploymentRuntimeScan{}, false, err
	}
	if row.InputHash != hash {
		return DeploymentRuntimeScan{}, false, ErrConflict
	}
	pointer, err := q.GetDeploymentRuntimeScanPointer(ctx, tx, mustPgUUID(in.DeploymentID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(pointer) != in.ID {
		return DeploymentRuntimeScan{}, false, ErrConflict
	}
	if err != nil {
		return DeploymentRuntimeScan{}, false, err
	}
	value, err := deploymentRuntimeScanRow(row)
	return value, true, err
}

func insertRuntimeScanTx(ctx context.Context, tx pgx.Tx, in DeploymentRuntimeScanInput, hash string, current DeploymentRuntimeProducerInputs) (DeploymentRuntimeScan, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	q := sqlc.New()
	if err := q.AuthorizeDeploymentRuntimeScanInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return DeploymentRuntimeScan{}, err
	}
	row, err := q.InsertDeploymentRuntimeScan(ctx, tx, sqlc.InsertDeploymentRuntimeScanParams{ID: mustPgUUID(in.ID),
		DeploymentID: mustPgUUID(in.DeploymentID), InputSnapshot: raw, InputHash: hash,
		PublisherExpiresAt: standardPgTime(current.ExpiresAt), TtlSeconds: api.ApplicationStandardArtifactScanTTL.Seconds(),
		DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRuntimeScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return DeploymentRuntimeScan{}, registryVerificationError(err)
	}
	value, err := deploymentRuntimeScanRow(row)
	if err != nil {
		return DeploymentRuntimeScan{}, err
	}
	if err := q.SelectDeploymentRuntimeScan(ctx, tx, sqlc.SelectDeploymentRuntimeScanParams{DeploymentID: mustPgUUID(in.DeploymentID), ID: mustPgUUID(in.ID)}); err != nil {
		return DeploymentRuntimeScan{}, registryVerificationError(err)
	}
	return value, nil
}

func currentRuntimeScanRow(ctx context.Context, db sqlc.DBTX, accountID, appID, depID string) (DeploymentRuntimeScan, error) {
	row, err := sqlc.New().GetCurrentDeploymentRuntimeScan(ctx, db, sqlc.GetCurrentDeploymentRuntimeScanParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)})
	if err != nil {
		return DeploymentRuntimeScan{}, registryVerificationError(err)
	}
	return deploymentRuntimeScanRow(row)
}

func (s *PgStore) GetCurrentDeploymentRuntimeScan(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeScan, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeScan{}, ErrInvalidArgument
	}
	return currentRuntimeScanRow(ctx, s.pool, accountID, appID, depID)
}

func (s *PgStore) GetFreshDeploymentRuntimeScan(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeScanEvidence, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeScanEvidence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	defer tx.Rollback(ctx)
	evidence, err := freshRuntimeScanTx(ctx, tx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	return evidence, nil
}

func freshRuntimeScanTx(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (DeploymentRuntimeScanEvidence, error) {
	current, err := readRuntimeProducerInputsTx(ctx, tx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	value, err := currentRuntimeScanRow(ctx, tx, accountID, appID, depID)
	if errors.Is(err, ErrNotFound) {
		return DeploymentRuntimeScanEvidence{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return DeploymentRuntimeScanEvidence{}, err
	}
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !now.Valid {
		return DeploymentRuntimeScanEvidence{}, errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	current.CheckedAt = now.Time
	return finishRuntimeScanEvidence(value, current)
}
