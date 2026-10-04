package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgSavedRouteRequirements(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.SavedRouteRequirements, error) {
	q := &sqlc.Queries{}
	body, err := q.ReadSavedRouteRequirements(ctx, db, sqlc.ReadSavedRouteRequirementsParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.SavedRouteRequirements{}, routePolicyReadError(err)
	}
	var saved api.SavedRouteRequirements
	if err := json.Unmarshal(body, &saved); err != nil {
		return saved, fmt.Errorf("decode saved route requirements: %w", err)
	}
	return saved, validateSavedRouteRequirements(saved, appID)
}

func (s *PgStore) GetSavedRouteRequirements(ctx context.Context, accountID, appID string) (api.SavedRouteRequirements, error) {
	return pgSavedRouteRequirements(ctx, s.pool, accountID, appID)
}

func (s *PgStore) SaveRouteRequirements(ctx context.Context, accountID, appID string, request api.SaveRouteRequirementsRequest) (api.SavedRouteRequirements, error) {
	config, digest, err := NormalizeSavedRouteRequirements(request)
	if err != nil {
		return api.SavedRouteRequirements{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.SavedRouteRequirements{}, fmt.Errorf("begin saved requirements update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.SavedRouteRequirements{}, routePolicyReadError(err)
	}
	current, err := pgSavedRouteRequirements(ctx, tx, accountID, appID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return current, err
	}
	if current.Revision != *request.ExpectedRevision || current.Revision >= api.RouteRequirementsMaxRevision && current.SHA256 != digest {
		return api.SavedRouteRequirements{}, ErrRouteRequirementsRevision
	}
	if current.SHA256 == digest {
		return current, tx.Commit(ctx)
	}
	body, err := json.Marshal(config)
	if err != nil {
		return api.SavedRouteRequirements{}, fmt.Errorf("encode saved requirements: %w", err)
	}
	if err := q.WriteSavedRouteRequirements(ctx, tx, sqlc.WriteSavedRouteRequirementsParams{AppID: appID, AccountID: accountID, Revision: current.Revision + 1, Sha256: digest, Requirements: body}); err != nil {
		return api.SavedRouteRequirements{}, fmt.Errorf("write saved requirements: %w", err)
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, accountID, appID)
	if err != nil {
		return saved, err
	}
	return saved, tx.Commit(ctx)
}

func (s *PgStore) CheckRouteRequirements(ctx context.Context, accountID, appID string, request api.CheckRouteRequirementsRequest, checker RouteRequirementsChecker) (api.RouteRequirementsCheck, error) {
	if err := ValidateCheckRouteRequirements(request); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteRequirementsCheck{}, fmt.Errorf("begin saved requirements check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, accountID, appID)
	if err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	if err := checkSavedRevision(saved, request.ExpectedRevision); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	if err := pgRoutePolicyContract(ctx, tx, &snapshot, request.DeploymentID, false); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	result, err := checker(snapshot, saved)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
