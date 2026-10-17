package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CheckedProjectReleaseStore = (*PgStore)(nil)

func (s *PgStore) ActivateProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID string, fallback []ProjectReleaseMember, ttl int, members []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttl) || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	ctx, err := checkedReleaseGraphContext(ctx, accountID, environment, members, fences)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	graphID := ctx.Value(checkedReleaseGraphKey{}).(string)
	byApp := map[string]string{}
	for _, member := range members {
		byApp[member.AppID] = member.DeploymentID
	}
	encodedMembers, err := json.Marshal(byApp)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	encodedFences, err := bindingReleaseFenceJSON(bindingReleaseFences(ctx))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before := func(ctx context.Context, tx pgx.Tx) error {
		if rollback, recovering := ctx.Value(checkedReleaseRollbackKey{}).(checkedReleaseRollback); recovering {
			data, err := sqlc.New().CheckedRollbackSourceForShare(ctx, tx, sqlc.CheckedRollbackSourceForShareParams{ID: mustPgUUID(rollback.source), AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			source, err := decodeProjectReleaseSet(data)
			if err != nil {
				return err
			}
			if err := validateCheckedRollbackSource(source, accountID, projectID, environment, byApp, time.Now()); err != nil {
				return err
			}
		}
		if err := pgAuthorizeRouteRemoval(ctx, tx); err != nil {
			return err
		}
		_, err := sqlc.New().AuthorizeBindingReleaseGraph(ctx, tx, sqlc.AuthorizeBindingReleaseGraphParams{ProjectID: mustPgUUID(projectID), Environment: environment, NewID: mustPgUUID(graphID), OldID: expectedActiveID, Members: encodedMembers, Fences: encodedFences})
		return mapErr(err)
	}
	release, err := publishProjectReleaseSetTx(ctx, tx, accountID, projectID, environment, &expectedActiveID, fallback, ttl, members, false, before)
	if err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("activate checked release: %w", checkedGraphWriteError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, fmt.Errorf("commit checked release: %w", checkedGraphWriteError(err))
	}
	return release, nil
}

func checkedGraphWriteError(err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && (postgres.Code == "40P01" || postgres.Code == "40001") {
		return ErrBindingPromotionChanged
	}
	return mapErr(err)
}

func (s *PgStore) RollbackProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID, sourceID string, ttl int, members []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	ctx, err := checkedReleaseRollbackContext(ctx, sourceID, expectedActiveID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	return s.ActivateProjectReleaseWithBindings(ctx, accountID, projectID, environment, expectedActiveID, nil, ttl, members, fences)
}

func (s *PgStore) DeactivateProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID string, fallback []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	ctx, err := checkedReleaseDeactivationContext(ctx, accountID, environment, expectedActiveID, fallback, fences)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	byApp := map[string]string{}
	for _, member := range fallback {
		byApp[member.AppID] = member.DeploymentID
	}
	selected, err := json.Marshal(byApp)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	encodedFences, err := bindingReleaseFenceJSON(bindingReleaseFences(ctx))
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	before := func(ctx context.Context, tx pgx.Tx) error {
		_, err := sqlc.New().AuthorizeBindingReleaseDeactivation(ctx, tx, sqlc.AuthorizeBindingReleaseDeactivationParams{ProjectID: mustPgUUID(projectID), Environment: environment, ReceiptID: mustPgUUID(ctx.Value(checkedReleaseGraphKey{}).(string)), OldID: expectedActiveID, Members: selected, Fences: encodedFences})
		return checkedGraphWriteError(err)
	}
	return s.deactivateProjectReleaseSet(ctx, accountID, projectID, environment, expectedActiveID, fallback, before)
}
