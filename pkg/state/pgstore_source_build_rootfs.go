package state

// adr: 435. This conversion record grants no scan, boot or adoption authority.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ SourceBuildRootfsStore = (*PgStore)(nil)

func sourceBuildRootfsRow(row sqlc.SourceBuildRootf) (SourceBuildRootfs, error) {
	var in SourceBuildRootfsInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return SourceBuildRootfs{}, err
	}
	in.ID = pgUUIDString(row.ID)
	value := SourceBuildRootfs{ID: in.ID, Input: in, InputHash: row.InputHash, PublishedAt: row.PublishedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if !row.PublishedAt.Valid || !row.ExpiresAt.Valid || in.PublicationID != pgUUIDString(row.PublicationID) || in.DeploymentID != pgUUIDString(row.DeploymentID) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err := validateSourceBuildRootfs(value); err != nil {
		return SourceBuildRootfs{}, err
	}
	return value, nil
}

func (s *PgStore) PublishSourceBuildRootfs(ctx context.Context, input SourceBuildRootfsInput) (SourceBuildRootfs, error) {
	in, hash, err := prepareSourceBuildRootfs(input)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockSourceBuildRootfsParents(ctx, tx, in); err != nil {
		return SourceBuildRootfs{}, err
	}
	value, err := saveSourceBuildRootfs(ctx, tx, in, hash)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceBuildRootfs{}, err
	}
	return value, nil
}

func lockSourceBuildRootfsParents(ctx context.Context, tx pgx.Tx, in SourceBuildRootfsInput) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	q := sqlc.New()
	raw, err = q.LockSourceBuildRootfs(ctx, tx, raw)
	if err != nil {
		return buildExportError(err)
	}
	var current struct {
		Intent    sourceBuildRootfsIntent `json:"intent"`
		Status    DeploymentStatus        `json:"status"`
		CheckedAt time.Time               `json:"checked_at"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	row, err := q.GetBuildExportPublicationByID(ctx, tx, mustPgUUID(in.PublicationID))
	if err != nil {
		return buildExportError(err)
	}
	parent, err := buildExportPublicationRow(row)
	if err != nil {
		return err
	}
	if current.CheckedAt.IsZero() {
		return ErrApplicationStandardRuntimeStale
	}
	if err := checkSourceBuildRootfsOwner(in, parent, current.Intent, current.Status, current.CheckedAt); err != nil {
		return err
	}
	// The SQL lock is a private owner fence, not cryptographic verification.
	now, err := lockBuildExportPublication(ctx, tx, parent.Input, true)
	if err != nil {
		return err
	}
	if parent.VerifiedAt.After(now) || !parent.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	return lockSourceBuildRootfsBase(ctx, tx, in, now)
}

func lockSourceBuildRootfsBase(ctx context.Context, tx pgx.Tx, in SourceBuildRootfsInput, now time.Time) error {
	q := sqlc.New()
	key := RuntimeBaseKeyForArch(in.Runtime, "amd64")
	if err := lockBaseImageProducerKeys(ctx, tx, key); err != nil {
		if errors.Is(err, ErrApplicationStandardReviewBusy) {
			return ErrApplicationStandardRuntimeBusy
		}
		return err
	}
	row, err := q.GetCurrentBaseImageProducer(ctx, tx, key)
	if err != nil {
		return buildExportError(err)
	}
	base, err := baseImageProducerRow(row)
	if err != nil {
		return err
	}
	if base.PublishedAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	return checkSourceBuildRootfsBase(in, base)
}

func saveSourceBuildRootfs(ctx context.Context, tx pgx.Tx, in SourceBuildRootfsInput, hash string) (SourceBuildRootfs, error) {
	q := sqlc.New()
	row, err := q.GetSourceBuildRootfsByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if row.InputHash != hash {
			return SourceBuildRootfs{}, ErrConflict
		}
		return retrySourceBuildRootfs(ctx, tx, in)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	if err := q.AuthorizeSourceBuildRootfsInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return SourceBuildRootfs{}, err
	}
	row, err = q.InsertSourceBuildRootfs(ctx, tx, sqlc.InsertSourceBuildRootfsParams{ID: mustPgUUID(in.ID), PublicationID: mustPgUUID(in.PublicationID), InputSnapshot: raw, InputHash: hash})
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	if err := stampSourceBuildRootfs(ctx, tx, in); err != nil {
		return SourceBuildRootfs{}, err
	}
	return sourceBuildRootfsRow(row)
}

func retrySourceBuildRootfs(ctx context.Context, tx pgx.Tx, in SourceBuildRootfsInput) (SourceBuildRootfs, error) {
	q := sqlc.New()
	pointer, err := q.GetSourceBuildRootfsPointer(ctx, tx, mustPgUUID(in.DeploymentID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(pointer) != in.ID {
		return SourceBuildRootfs{}, ErrConflict
	}
	if err != nil {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	row, err := q.GetCurrentSourceBuildRootfs(ctx, tx, sourceBuildRootfsScope(in.AccountID, in.AppID, in.DeploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	return sourceBuildRootfsRow(row)
}

func stampSourceBuildRootfs(ctx context.Context, tx pgx.Tx, in SourceBuildRootfsInput) error {
	q := sqlc.New()
	count, err := q.PublishDeploymentRegistryMainRootfs(ctx, tx, sqlc.PublishDeploymentRegistryMainRootfsParams{DeploymentID: mustPgUUID(in.DeploymentID), RootfsPath: in.RootfsPath, StorageKey: in.StorageKey, ContentBytes: in.ContentBytes})
	if err != nil {
		return buildExportError(err)
	}
	if count != 1 {
		return ErrApplicationStandardRuntimeStale
	}
	return buildExportError(q.SelectSourceBuildRootfs(ctx, tx, sqlc.SelectSourceBuildRootfsParams{DeploymentID: mustPgUUID(in.DeploymentID), ID: mustPgUUID(in.ID)}))
}

func sourceBuildRootfsScope(accountID, appID, depID string) sqlc.GetCurrentSourceBuildRootfsParams {
	return sqlc.GetCurrentSourceBuildRootfsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)}
}

func (s *PgStore) GetCurrentSourceBuildRootfs(ctx context.Context, accountID, appID, depID string) (SourceBuildRootfs, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return SourceBuildRootfs{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentSourceBuildRootfs(ctx, s.pool, sourceBuildRootfsScope(accountID, appID, depID))
	if err != nil {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	return sourceBuildRootfsRow(row)
}
