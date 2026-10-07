package state

// adr: 435

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentRegistryRootfsStore = (*PgStore)(nil)

func registryRootfsRow(row sqlc.DeploymentRegistryRootf) (DeploymentRegistryRootfs, error) {
	var in DeploymentRegistryRootfsInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	in.ID = pgUUIDString(row.ID)
	value := DeploymentRegistryRootfs{ID: in.ID, Input: in, InputHash: row.InputHash, PublishedAt: row.PublishedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if !row.PublishedAt.Valid || !row.ExpiresAt.Valid || in.RegistryVerificationID != pgUUIDString(row.RegistryVerificationID) ||
		in.DeploymentID != pgUUIDString(row.DeploymentID) || in.WorkloadName != row.WorkloadName {
		return DeploymentRegistryRootfs{}, fmt.Errorf("registry rootfs stored owner mismatch")
	}
	if err := validateRegistryRootfsStored(value); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return value, nil
}

func lockRegistryRootfsParent(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput) (DeploymentRegistryVerification, error) {
	q := sqlc.New()
	raw, err := q.LockDeploymentRegistryRootfs(ctx, tx, mustPgUUID(in.RegistryVerificationID))
	if err != nil {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	var current struct {
		AppID          string           `json:"app_id"`
		AccountID      string           `json:"account_id"`
		OrgID          string           `json:"org_id"`
		ImageReference string           `json:"image_reference"`
		KeyDER         []byte           `json:"key_der"`
		Scope          string           `json:"scope"`
		Status         DeploymentStatus `json:"status"`
		Now            time.Time        `json:"storage_now"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	row, err := q.GetDeploymentRegistryVerificationByID(ctx, tx, mustPgUUID(in.RegistryVerificationID))
	if err != nil {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	parent, err := registryVerificationRow(row)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if current.Now.IsZero() || current.AppID != in.AppID || current.AccountID != in.AccountID || current.OrgID != in.OrgID || current.ImageReference != parent.Input.ImageReference {
		return DeploymentRegistryVerification{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkRegistryRootfsParent(in, parent, Deployment{Scope: current.Scope, Status: current.Status}, current.Now); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if err := verifyRegistryCurrentKey(parent.Input, current.KeyDER); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	return parent, nil
}

func (s *PgStore) PublishDeploymentRegistryRootfs(ctx context.Context, input DeploymentRegistryRootfsInput) (DeploymentRegistryRootfs, error) {
	in, hash, err := prepareRegistryRootfs(input)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := publishDeploymentRegistryRootfsTx(ctx, tx, in, hash)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return value, nil
}

func publishDeploymentRegistryRootfsTx(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, hash string) (DeploymentRegistryRootfs, error) {
	parent, err := lockRegistryRootfsParent(ctx, tx, in)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if err := lockRegistryRootfsBase(ctx, tx, in, parent); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	q := sqlc.New()
	existing, err := q.GetDeploymentRegistryRootfsByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if existing.InputHash != hash {
			return DeploymentRegistryRootfs{}, ErrConflict
		}
		return retryRegistryRootfs(ctx, tx, in, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	return insertRegistryRootfs(ctx, tx, in, hash, parent)
}

func retryRegistryRootfs(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, existing sqlc.DeploymentRegistryRootf) (DeploymentRegistryRootfs, error) {
	q := sqlc.New()
	pointer, err := q.GetDeploymentRegistryRootfsPointer(ctx, tx, sqlc.GetDeploymentRegistryRootfsPointerParams{
		DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(pointer) != in.ID {
		return DeploymentRegistryRootfs{}, ErrConflict
	}
	if err != nil {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	selected, err := q.GetCurrentDeploymentRegistryRootfs(ctx, tx, sqlc.GetCurrentDeploymentRegistryRootfsParams{
		AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(selected.ID) != in.ID {
		return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	value, err := registryRootfsRow(existing)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return value, nil
}

func insertRegistryRootfs(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, hash string, parent DeploymentRegistryVerification) (DeploymentRegistryRootfs, error) {
	q := sqlc.New()
	raw, err := json.Marshal(in)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if err := q.AuthorizeDeploymentRegistryRootfsInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	row, err := q.InsertDeploymentRegistryRootfs(ctx, tx, sqlc.InsertDeploymentRegistryRootfsParams{
		ID: mustPgUUID(in.ID), VerificationID: mustPgUUID(in.RegistryVerificationID), InputSnapshot: raw, InputHash: hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	if err := publishRegistryRootfsMetadata(ctx, tx, in, parent.Input.SelectedReference); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if err := q.SelectDeploymentRegistryRootfs(ctx, tx, sqlc.SelectDeploymentRegistryRootfsParams{
		DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName, ID: mustPgUUID(in.ID),
	}); err != nil {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	value, err := registryRootfsRow(row)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return value, nil
}

func publishRegistryRootfsMetadata(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, selected string) error {
	q := sqlc.New()
	if in.WorkloadName != "" {
		return registryVerificationError(q.PublishDeploymentRegistrySidecarRootfs(ctx, tx, sqlc.PublishDeploymentRegistrySidecarRootfsParams{
			DeploymentID: mustPgUUID(in.DeploymentID), WorkloadName: in.WorkloadName, StorageKey: in.StorageKey, ContentBytes: in.ContentBytes, SelectedReference: selected,
		}))
	}
	count, err := q.PublishDeploymentRegistryMainRootfs(ctx, tx, sqlc.PublishDeploymentRegistryMainRootfsParams{
		DeploymentID: mustPgUUID(in.DeploymentID), RootfsPath: in.RootfsPath, StorageKey: in.StorageKey, ContentBytes: in.ContentBytes,
	})
	if err != nil {
		return registryVerificationError(err)
	}
	if count != 1 {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (s *PgStore) GetCurrentDeploymentRegistryRootfs(ctx context.Context, accountID, appID, depID, workload string) (DeploymentRegistryRootfs, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return DeploymentRegistryRootfs{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentDeploymentRegistryRootfs(ctx, s.pool, sqlc.GetCurrentDeploymentRegistryRootfsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: workload,
	})
	if err != nil {
		return DeploymentRegistryRootfs{}, registryVerificationError(err)
	}
	return registryRootfsRow(row)
}
