package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

var _ BuildExportPublicationStore = (*PgStore)(nil)

func (s *PgStore) HasBuildExportPublication(ctx context.Context, accountID, appID, depID string) (bool, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return false, ErrInvalidArgument
	}
	return sqlc.New().HasScopedBuildExportPublication(ctx, s.pool, sqlc.HasScopedBuildExportPublicationParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)})
}

func buildExportError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.ConstraintName {
		case "build_export_publication_missing":
			return ErrNotFound
		case "build_export_publication_stale":
			return ErrApplicationStandardRuntimeStale
		case "build_export_publication_busy":
			return ErrApplicationStandardRuntimeBusy
		case "build_export_publication_conflict":
			return ErrConflict
		case "build_export_publication_immutable":
			return ErrInvalidArgument
		}
	}
	return mapErr(err)
}

func buildExportPublicationRow(row sqlc.BuildExportPublication) (BuildExportPublication, error) {
	var in BuildExportPublicationInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return BuildExportPublication{}, err
	}
	in.ID = pgUUIDString(row.ID)
	in.Proof.Payload = append([]byte(nil), row.Payload...)
	in.Proof.Signature = append([]byte(nil), row.Signature...)
	if in.Claims.AccountID != pgUUIDString(row.AccountID) || in.Claims.AppID != pgUUIDString(row.AppID) || in.Claims.DeploymentID != pgUUIDString(row.DeploymentID) || in.Claims.BuildID != pgUUIDString(row.BuildID) || !row.VerifiedAt.Valid || !row.ExpiresAt.Valid {
		return BuildExportPublication{}, ErrApplicationStandardRuntimeStale
	}
	value := BuildExportPublication{ID: in.ID, InputHash: row.InputHash, Input: in, VerifiedAt: row.VerifiedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if err := validateBuildExportPublication(value); err != nil {
		return BuildExportPublication{}, err
	}
	return value, nil
}

func lockBuildExportPublication(ctx context.Context, tx pgx.Tx, in BuildExportPublicationInput, fresh bool) (time.Time, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return time.Time{}, err
	}
	raw, err = sqlc.New().LockBuildExportPublication(ctx, tx, sqlc.LockBuildExportPublicationParams{Input: raw, Publisher: in.Proof.PublisherName, Fresh: fresh})
	if err != nil {
		return time.Time{}, buildExportError(err)
	}
	var current struct {
		KeyDER    []byte    `json:"key_der"`
		CheckedAt time.Time `json:"checked_at"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return time.Time{}, err
	}
	if err := buildpublisher.Verify(in.Claims, in.Proof, current.KeyDER); err != nil {
		return time.Time{}, err
	}
	if current.CheckedAt.IsZero() {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	return current.CheckedAt, nil
}

func (s *PgStore) RecordBuildExportPublication(ctx context.Context, input BuildExportPublicationInput) (BuildExportPublication, error) {
	in, hash, err := prepareBuildExportPublication(input)
	if err != nil {
		return BuildExportPublication{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BuildExportPublication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockBuildExportPublication(ctx, tx, in, false); err != nil {
		return BuildExportPublication{}, err
	}
	q := sqlc.New()
	old, err := q.GetBuildExportPublicationByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if old.InputHash != hash {
			return BuildExportPublication{}, ErrConflict
		}
		return buildExportPublicationRow(old)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BuildExportPublication{}, err
	}
	first, err := q.GetFirstBuildExportPublicationForClaim(ctx, tx, sqlc.GetFirstBuildExportPublicationForClaimParams{BuildID: mustPgUUID(in.Claims.BuildID), ClaimStartedAt: in.Claims.ClaimStartedAt})
	if err == nil {
		value, parseErr := buildExportPublicationRow(first)
		if parseErr != nil {
			return BuildExportPublication{}, parseErr
		}
		if value.Input.Claims != in.Claims {
			return BuildExportPublication{}, ErrConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return BuildExportPublication{}, err
	}
	value, err := insertBuildExportPublication(ctx, tx, in, hash)
	if err != nil {
		return BuildExportPublication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BuildExportPublication{}, err
	}
	return value, nil
}

func insertBuildExportPublication(ctx context.Context, tx pgx.Tx, in BuildExportPublicationInput, hash string) (BuildExportPublication, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return BuildExportPublication{}, err
	}
	q := sqlc.New()
	if err := q.AuthorizeBuildExportPublicationInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return BuildExportPublication{}, err
	}
	row, err := q.InsertBuildExportPublication(ctx, tx, sqlc.InsertBuildExportPublicationParams{ID: mustPgUUID(in.ID), BuildID: mustPgUUID(in.Claims.BuildID), DeploymentID: mustPgUUID(in.Claims.DeploymentID), AppID: mustPgUUID(in.Claims.AppID), AccountID: mustPgUUID(in.Claims.AccountID), InputSnapshot: raw, InputHash: hash, Payload: in.Proof.Payload, Signature: in.Proof.Signature, TtlSeconds: api.BuildExportPublicationVerificationTTL.Seconds()})
	if err != nil {
		return BuildExportPublication{}, buildExportError(err)
	}
	return buildExportPublicationRow(row)
}

func (s *PgStore) GetFreshBuildExportPublication(ctx context.Context, accountID, appID, depID, buildID string) (BuildExportPublication, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, buildID) {
		return BuildExportPublication{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BuildExportPublication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().GetLatestScopedBuildExportPublication(ctx, tx, sqlc.GetLatestScopedBuildExportPublicationParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), BuildID: mustPgUUID(buildID)})
	if err != nil {
		return BuildExportPublication{}, buildExportError(err)
	}
	value, err := buildExportPublicationRow(row)
	if err != nil {
		return BuildExportPublication{}, err
	}
	now, err := lockBuildExportPublication(ctx, tx, value.Input, true)
	if err != nil {
		return BuildExportPublication{}, err
	}
	if value.VerifiedAt.After(now) || !value.ExpiresAt.After(now) {
		return BuildExportPublication{}, ErrApplicationStandardRuntimeStale
	}
	if err := tx.Commit(ctx); err != nil {
		return BuildExportPublication{}, err
	}
	return value, nil
}
