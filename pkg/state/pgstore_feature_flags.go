package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func flagPGScope(s FeatureFlagScope) (sqlc.LockFeatureFlagEnvironmentParams, error) {
	var out sqlc.LockFeatureFlagEnvironmentParams
	for _, pair := range []struct {
		id   string
		dest *pgtype.UUID
	}{{s.AccountID, &out.AccountID}, {s.ProjectID, &out.ProjectID}, {s.EnvironmentID, &out.EnvironmentID}} {
		u, err := uuid.Parse(pair.id)
		if err != nil {
			return out, ErrInvalidArgument
		}
		*pair.dest = pgtype.UUID{Bytes: u, Valid: true}
	}
	return out, nil
}
func flagPGRow(row sqlc.FeatureFlagVersion) (FeatureFlagVersion, error) {
	v := emptyFeatureFlags(FeatureFlagScope{EnvironmentID: uuidString(row.EnvironmentID)})
	if err := json.Unmarshal(row.Config, &v.Config); err != nil {
		return FeatureFlagVersion{}, fmt.Errorf("decode flags: %w", err)
	}
	v.Version, v.Actor, v.CreatedAt = row.Version, row.Actor, row.CreatedAt.Time
	if row.RestoredFrom.Valid {
		v.RestoredFrom = row.RestoredFrom.Int64
	}
	return v, nil
}
func flagPGGet(ctx context.Context, db sqlc.DBTX, s FeatureFlagScope, p sqlc.LockFeatureFlagEnvironmentParams, version int64) (FeatureFlagVersion, error) {
	row, err := sqlc.New().GetFeatureFlagVersion(ctx, db, sqlc.GetFeatureFlagVersionParams{AccountID: p.AccountID, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		if version == 0 {
			return emptyFeatureFlags(s), nil
		}
		return FeatureFlagVersion{}, ErrNotFound
	}
	if err != nil {
		return FeatureFlagVersion{}, fmt.Errorf("read flags: %w", err)
	}
	return flagPGRow(row)
}
func (s *PgStore) GetFeatureFlags(ctx context.Context, scope FeatureFlagScope, version int64) (FeatureFlagVersion, error) {
	p, err := flagPGScope(scope)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	// A short transaction checks ownership and serializes with publication/deletion.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = sqlc.New().LockFeatureFlagEnvironment(ctx, tx, p); errors.Is(err, pgx.ErrNoRows) {
		return FeatureFlagVersion{}, ErrNotFound
	} else if err != nil {
		return FeatureFlagVersion{}, err
	}
	return flagPGGet(ctx, tx, scope, p, version)
}
func (s *PgStore) ListFeatureFlagVersions(ctx context.Context, scope FeatureFlagScope, before int64) ([]FeatureFlagVersion, error) {
	p, err := flagPGScope(scope)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetFeatureFlags(ctx, scope, 0); err != nil {
		return nil, err
	}
	if before == 0 {
		before = math.MaxInt64
	}
	rows, err := sqlc.New().ListFeatureFlagVersions(ctx, s.pool, sqlc.ListFeatureFlagVersionsParams{AccountID: p.AccountID, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, BeforeVersion: before})
	if err != nil {
		return nil, err
	}
	out := []FeatureFlagVersion{}
	for _, row := range rows {
		v, err := flagPGRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *PgStore) ListFeatureFlagAutoRolloutCandidates(ctx context.Context, afterEnvironmentID string, limit int) ([]FeatureFlagAutoRolloutCandidate, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	var after pgtype.UUID
	if afterEnvironmentID != "" {
		id, err := uuid.Parse(afterEnvironmentID)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		after = pgtype.UUID{Bytes: id, Valid: true}
	}
	rows, err := sqlc.New().ListFeatureFlagAutoRolloutCandidates(ctx, s.pool, sqlc.ListFeatureFlagAutoRolloutCandidatesParams{
		AfterEnvironmentID: after,
		LimitRows:          int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list automatic feature flag rollouts: %w", err)
	}
	candidates := make([]FeatureFlagAutoRolloutCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, FeatureFlagAutoRolloutCandidate{
			Scope: FeatureFlagScope{
				AccountID:     uuidString(row.AccountID),
				ProjectID:     uuidString(row.ProjectID),
				EnvironmentID: uuidString(row.EnvironmentID),
			},
			ProjectSlug:     row.ProjectSlug,
			EnvironmentSlug: row.EnvironmentSlug,
		})
	}
	return candidates, nil
}

func (s *PgStore) UpdateFeatureFlags(ctx context.Context, u FeatureFlagUpdate) (FeatureFlagVersion, error) {
	p, err := flagPGScope(u.Scope)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// The version insert takes a project FK lock. Take it before the
	// environment lock to match clone capture and project deletion order.
	if _, err = q.LockFeatureFlagProject(ctx, tx, sqlc.LockFeatureFlagProjectParams{AccountID: p.AccountID, ProjectID: p.ProjectID}); errors.Is(err, pgx.ErrNoRows) {
		return FeatureFlagVersion{}, ErrNotFound
	} else if err != nil {
		return FeatureFlagVersion{}, err
	}
	if _, err = q.LockFeatureFlagEnvironment(ctx, tx, p); errors.Is(err, pgx.ErrNoRows) {
		return FeatureFlagVersion{}, ErrNotFound
	} else if err != nil {
		return FeatureFlagVersion{}, err
	}
	prior, err := flagPGGet(ctx, tx, u.Scope, p, 0)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	if u.RestoreVersion > 0 {
		old, err := flagPGGet(ctx, tx, u.Scope, p, u.RestoreVersion)
		if err != nil {
			return FeatureFlagVersion{}, err
		}
		u.Config = old.Config
	}
	v, err := prepareFeatureFlags(u, prior)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	for _, id := range flagCustomerIDs(v.Config) {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return FeatureFlagVersion{}, ErrInvalidArgument
		}
		owned, err := q.FeatureFlagCustomerOwned(ctx, tx, sqlc.FeatureFlagCustomerOwnedParams{AccountID: p.AccountID, TenantID: pgtype.UUID{Bytes: parsed, Valid: true}})
		if err != nil {
			return FeatureFlagVersion{}, err
		}
		if !owned {
			return FeatureFlagVersion{}, ErrNotFound
		}
	}
	raw, err := json.Marshal(v.Config)
	if err != nil {
		return FeatureFlagVersion{}, err
	}
	row, err := q.InsertFeatureFlagVersion(ctx, tx, sqlc.InsertFeatureFlagVersionParams{AccountID: p.AccountID, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, Version: v.Version, Config: raw, Actor: u.Actor, RestoredFrom: pgtype.Int8{Int64: u.RestoreVersion, Valid: u.RestoreVersion > 0}})
	if err != nil {
		return FeatureFlagVersion{}, fmt.Errorf("publish flags: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return FeatureFlagVersion{}, fmt.Errorf("commit flags: %w", err)
	}
	return flagPGRow(row)
}
