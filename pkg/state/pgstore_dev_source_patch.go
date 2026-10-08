package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DevSourcePatchStore = (*PgStore)(nil)

// devSourcePatchInsertAttempts bounds retries when two uploads for the same
// base deployment race for the next generation.
const devSourcePatchInsertAttempts = 3

func (s *PgStore) RecordDevSourceManifest(ctx context.Context, manifest DevSourceManifest, keep int) error {
	deploymentID, err := parsePgUUID(manifest.DeploymentID)
	if err != nil {
		return err
	}
	appID, err := parsePgUUID(manifest.AppID)
	if err != nil {
		return err
	}
	entries, err := json.Marshal(manifest.Entries)
	if err != nil {
		return fmt.Errorf("state: encode developer source manifest: %w", err)
	}
	q := sqlc.New()
	if err := q.UpsertDevSourceManifest(ctx, s.pool, sqlc.UpsertDevSourceManifestParams{
		DeploymentID: deploymentID, AppID: appID, SourceRoot: manifest.SourceRoot, Manifest: entries,
	}); err != nil {
		return fmt.Errorf("state: record developer source manifest: %w", err)
	}
	if _, err := q.PruneDevSourceManifests(ctx, s.pool, sqlc.PruneDevSourceManifestsParams{AppID: appID, KeepCount: int32(keep)}); err != nil {
		return fmt.Errorf("state: prune developer source manifests: %w", err)
	}
	return nil
}

func (s *PgStore) DevSourceManifest(ctx context.Context, deploymentID string) (DevSourceManifest, error) {
	id, err := parsePgUUID(deploymentID)
	if err != nil {
		return DevSourceManifest{}, ErrNotFound
	}
	row, err := sqlc.New().GetDevSourceManifest(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return DevSourceManifest{}, ErrNotFound
	}
	if err != nil {
		return DevSourceManifest{}, fmt.Errorf("state: load developer source manifest: %w", err)
	}
	manifest := DevSourceManifest{
		DeploymentID: pgUUIDString(row.DeploymentID), AppID: pgUUIDString(row.AppID),
		SourceRoot: row.SourceRoot, CreatedAt: row.CreatedAt.Time.UTC(),
	}
	if err := json.Unmarshal(row.Manifest, &manifest.Entries); err != nil {
		return DevSourceManifest{}, fmt.Errorf("state: decode developer source manifest: %w", err)
	}
	return manifest, nil
}

func (s *PgStore) CreateDevSourcePatch(ctx context.Context, patch DevSourcePatch) (DevSourcePatch, error) {
	appID, err := parsePgUUID(patch.AppID)
	if err != nil {
		return DevSourcePatch{}, err
	}
	baseID, err := parsePgUUID(patch.BaseDeploymentID)
	if err != nil {
		return DevSourcePatch{}, err
	}
	deleted, err := json.Marshal(nonNilStrings(patch.Deleted))
	if err != nil {
		return DevSourcePatch{}, fmt.Errorf("state: encode developer patch deletions: %w", err)
	}
	q := sqlc.New()
	if _, err := q.PruneDevSourcePatches(ctx, s.pool, sqlc.PruneDevSourcePatchesParams{AppID: appID, BaseDeploymentID: baseID}); err != nil {
		return DevSourcePatch{}, fmt.Errorf("state: prune developer patches: %w", err)
	}
	for attempt := 1; ; attempt++ {
		row, err := q.InsertDevSourcePatch(ctx, s.pool, sqlc.InsertDevSourcePatchParams{
			AppID: appID, BaseDeploymentID: baseID, ImageDir: patch.ImageDir, Archive: patch.Archive,
			Deleted: deleted, Digest: patch.Digest, ExpiresAt: pgtype.Timestamptz{Time: patch.ExpiresAt, Valid: true},
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation && attempt < devSourcePatchInsertAttempts {
			continue
		}
		if err != nil {
			return DevSourcePatch{}, fmt.Errorf("state: insert developer patch: %w", err)
		}
		patch.ID = pgUUIDString(row.ID)
		patch.Generation = row.Generation
		patch.CreatedAt = row.CreatedAt.Time.UTC()
		return patch, nil
	}
}

func (s *PgStore) LatestDevSourcePatch(ctx context.Context, appID, baseDeploymentID string, afterGeneration int64) (DevSourcePatch, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return DevSourcePatch{}, ErrNotFound
	}
	base, err := parsePgUUID(baseDeploymentID)
	if err != nil {
		return DevSourcePatch{}, ErrNotFound
	}
	row, err := sqlc.New().LatestDevSourcePatch(ctx, s.pool, sqlc.LatestDevSourcePatchParams{
		AppID: app, BaseDeploymentID: base, AfterGeneration: afterGeneration,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DevSourcePatch{}, ErrNotFound
	}
	if err != nil {
		return DevSourcePatch{}, fmt.Errorf("state: load developer patch: %w", err)
	}
	patch := DevSourcePatch{
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), BaseDeploymentID: pgUUIDString(row.BaseDeploymentID),
		Generation: row.Generation, ImageDir: row.ImageDir, Archive: row.Archive, Digest: row.Digest,
		CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
	}
	if err := json.Unmarshal(row.Deleted, &patch.Deleted); err != nil {
		return DevSourcePatch{}, fmt.Errorf("state: decode developer patch deletions: %w", err)
	}
	return patch, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
