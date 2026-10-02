package state

// adr: 429

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentArtifactScanStore = (*PgStore)(nil)

func artifactScanRow(row sqlc.DeploymentArtifactScan) (DeploymentArtifactScan, error) {
	var in DeploymentArtifactScanInput
	var result api.ScanResult
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return DeploymentArtifactScan{}, err
	}
	if err := json.Unmarshal(row.ResultSnapshot, &result); err != nil {
		return DeploymentArtifactScan{}, err
	}
	in.ID = pgUUIDString(row.ID)
	clock, err := time.Parse(time.RFC3339Nano, result.ScannedAt)
	if err != nil || !row.ScannedAt.Valid || !row.ExpiresAt.Valid || !clock.Equal(row.ScannedAt.Time) || in.RootfsProducerID != pgUUIDString(row.RootfsProducerID) || in.DeploymentID != pgUUIDString(row.DeploymentID) || in.WorkloadName != row.WorkloadName {
		return DeploymentArtifactScan{}, fmt.Errorf("artifact scan stored owner/clock mismatch")
	}
	if in.RegistryVerificationID == "" && row.RegistryVerificationID.Valid || in.RegistryVerificationID != "" && (!row.RegistryVerificationID.Valid || pgUUIDString(row.RegistryVerificationID) != in.RegistryVerificationID) {
		return DeploymentArtifactScan{}, fmt.Errorf("artifact scan stored approval mismatch")
	}
	result.ScannedAt = row.ScannedAt.Time.UTC().Format(time.RFC3339Nano)
	value := DeploymentArtifactScan{ID: in.ID, Input: in, InputHash: row.InputHash, Result: result, ScannedAt: row.ScannedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if err := validateDeploymentArtifactScan(value); err != nil {
		return DeploymentArtifactScan{}, err
	}
	return value, nil
}

func lockArtifactScanParent(ctx context.Context, tx pgx.Tx, in DeploymentArtifactScanInput) (artifactScanParents, error) {
	q := sqlc.New()
	params := sqlc.LockDeploymentArtifactScanParams{ProducerID: mustPgUUID(in.RootfsProducerID)}
	if in.RegistryVerificationID != "" {
		params.VerificationID = mustPgUUID(in.RegistryVerificationID)
	}
	raw, err := q.LockDeploymentArtifactScan(ctx, tx, params)
	if err != nil {
		return artifactScanParents{}, registryVerificationError(err)
	}
	var current struct {
		AppID          string `json:"app_id"`
		AccountID      string `json:"account_id"`
		OrgID          string `json:"org_id"`
		ImageReference string `json:"image_reference"`
		KeyDER         []byte `json:"key_der"`
		Scope          string
		Status         DeploymentStatus
		Now            time.Time `json:"storage_now"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return artifactScanParents{}, err
	}
	row, err := q.GetCurrentDeploymentRegistryRootfs(ctx, tx, sqlc.GetCurrentDeploymentRegistryRootfsParams{
		AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(row.ID) != in.RootfsProducerID {
		return artifactScanParents{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return artifactScanParents{}, registryVerificationError(err)
	}
	root, err := registryRootfsRow(row)
	if err != nil {
		return artifactScanParents{}, err
	}
	parentRow, err := q.GetDeploymentRegistryVerificationByID(ctx, tx, row.RegistryVerificationID)
	if err != nil {
		return artifactScanParents{}, registryVerificationError(err)
	}
	origin, err := registryVerificationRow(parentRow)
	if err != nil {
		return artifactScanParents{}, err
	}
	parent := origin
	if in.RegistryVerificationID != "" {
		parentRow, err = q.GetDeploymentRegistryVerificationByID(ctx, tx, mustPgUUID(in.RegistryVerificationID))
		if err != nil {
			return artifactScanParents{}, registryVerificationError(err)
		}
		parent, err = registryVerificationRow(parentRow)
		if err != nil {
			return artifactScanParents{}, err
		}
	}
	if current.Now.IsZero() || current.AppID != in.AppID || current.AccountID != in.AccountID || current.OrgID != in.OrgID || current.ImageReference != in.ImageReference {
		return artifactScanParents{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkArtifactScanParent(in, root, origin, parent, Deployment{Scope: current.Scope, Status: current.Status}, current.Now); err != nil {
		return artifactScanParents{}, err
	}
	if err := verifyRegistryCurrentKey(parent.Input, current.KeyDER); err != nil {
		return artifactScanParents{}, err
	}
	if err := lockRegistryRootfsBase(ctx, tx, root.Input, parent); err != nil {
		return artifactScanParents{}, err
	}
	return artifactScanParents{Rootfs: root, Approval: parent}, nil
}

func (s *PgStore) PublishDeploymentArtifactScan(ctx context.Context, input DeploymentArtifactScanInput) (DeploymentArtifactScan, error) {
	in, hash, err := prepareDeploymentArtifactScan(input)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockArtifactScanParent(ctx, tx, in); err != nil {
		return DeploymentArtifactScan{}, err
	}
	q := sqlc.New()
	existing, err := q.GetDeploymentArtifactScanByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if existing.InputHash != hash {
			return DeploymentArtifactScan{}, ErrConflict
		}
		pointer, err := q.GetDeploymentArtifactScanPointer(ctx, tx, sqlc.GetDeploymentArtifactScanPointerParams{DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(pointer) != in.ID {
			return DeploymentArtifactScan{}, ErrConflict
		}
		if err != nil {
			return DeploymentArtifactScan{}, registryVerificationError(err)
		}
		value, err := artifactScanRow(existing)
		if err != nil {
			return DeploymentArtifactScan{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DeploymentArtifactScan{}, err
		}
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentArtifactScan{}, registryVerificationError(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	if err := q.AuthorizeDeploymentArtifactScanInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return DeploymentArtifactScan{}, err
	}
	row, err := q.InsertDeploymentArtifactScan(ctx, tx, sqlc.InsertDeploymentArtifactScanParams{
		ID: mustPgUUID(in.ID), ProducerID: mustPgUUID(in.RootfsProducerID), InputSnapshot: raw, InputHash: hash,
		TtlSeconds: api.ApplicationStandardArtifactScanTTL.Seconds(), DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentArtifactScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return DeploymentArtifactScan{}, registryVerificationError(err)
	}
	value, err := artifactScanRow(row)
	if err != nil {
		return DeploymentArtifactScan{}, err
	}
	if in.WorkloadName == "" {
		result, err := json.Marshal(value.Result)
		if err != nil {
			return DeploymentArtifactScan{}, err
		}
		count, err := q.PublishDeploymentArtifactMainScan(ctx, tx, sqlc.PublishDeploymentArtifactMainScanParams{
			DeploymentID: mustPgUUID(in.DeploymentID), ResultSnapshot: result, Status: in.Status, ScannedAt: row.ScannedAt,
		})
		if err != nil {
			return DeploymentArtifactScan{}, registryVerificationError(err)
		}
		if count != 1 {
			return DeploymentArtifactScan{}, ErrApplicationStandardRuntimeStale
		}
	}
	if err := q.SelectDeploymentArtifactScan(ctx, tx, sqlc.SelectDeploymentArtifactScanParams{DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName, ID: mustPgUUID(in.ID)}); err != nil {
		return DeploymentArtifactScan{}, registryVerificationError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentArtifactScan{}, err
	}
	return value, nil
}

func (s *PgStore) GetCurrentDeploymentArtifactScan(ctx context.Context, accountID, appID, depID, workload string) (DeploymentArtifactScan, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) || workload != "" && !api.ValidSidecarName(workload) {
		return DeploymentArtifactScan{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentDeploymentArtifactScan(ctx, s.pool, sqlc.GetCurrentDeploymentArtifactScanParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: workload,
	})
	if err != nil {
		return DeploymentArtifactScan{}, registryVerificationError(err)
	}
	return artifactScanRow(row)
}
