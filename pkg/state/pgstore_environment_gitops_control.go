package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsControlStore = (*PgStore)(nil)

func validateGitOpsOverride(request EnvironmentGitOpsOverrideRequest, now time.Time) error {
	if request.Resource == "" || request.Path == "" || strings.Contains(request.Resource+request.Path, "#") ||
		strings.TrimSpace(request.Reason) == "" || len(request.Reason) > api.EnvironmentGitOpsMaxOverrideReasonBytes ||
		!request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(api.EnvironmentGitOpsMaxOverrideDuration)) {
		return ErrInvalidArgument
	}
	return nil
}

func changedEnvironmentGitSource(source EnvironmentGitSource, update EnvironmentGitSourceUpdate) (EnvironmentGitSource, error) {
	if source.Detached || source.Generation != update.ExpectedGeneration {
		return source, ErrConflict
	}
	if update.Mode != "" {
		if update.Mode != "report" && update.Mode != "enforce" {
			return source, ErrInvalidArgument
		}
		source.Spec.Mode = update.Mode
	}
	if update.Prune != nil {
		source.Spec.Prune = *update.Prune
	}
	if update.Suspended != nil {
		source.Suspended = *update.Suspended
	}
	return source, nil
}

func recordGitOpsEvent(ctx context.Context, tx pgx.Tx, sourceID, actor, kind string, details any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return ErrInvalidArgument
	}
	return sqlc.New().RecordEnvironmentGitOpsEvent(ctx, tx, sqlc.RecordEnvironmentGitOpsEventParams{SourceID: mustPgUUID(sourceID), Actor: actor, Kind: kind, Details: raw})
}

func enqueueGitOpsControl(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource) error {
	q := sqlc.New()
	if err := q.TouchEnvironmentGitOpsIntent(ctx, tx, mustPgUUID(source.ID)); err != nil {
		return err
	}
	if source.Generation == 0 || source.ApprovedRevisionID == "" {
		return nil
	}
	return q.EnqueueEnvironmentGitOps(ctx, tx, sqlc.EnqueueEnvironmentGitOpsParams{SourceID: mustPgUUID(source.ID), Generation: source.Generation, NextAttemptAt: gitOpsTime(time.Now())})
}

func (s *PgStore) withGitOpsControl(ctx context.Context, accountID, sourceID string, operation func(pgx.Tx, EnvironmentGitSource) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{AccountID: mustPgUUID(accountID), SourceID: mustPgUUID(sourceID)})
	if err != nil {
		return mapErr(err)
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, row.ID)
	if err != nil {
		return mapErr(err)
	}
	if err := operation(tx, environmentGitSourceFromSQL(row, scope.EnvironmentSlug)); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) UpdateEnvironmentGitSource(ctx context.Context, accountID, sourceID string, update EnvironmentGitSourceUpdate) (EnvironmentGitSource, error) {
	var result EnvironmentGitSource
	err := s.withGitOpsControl(ctx, accountID, sourceID, func(tx pgx.Tx, source EnvironmentGitSource) error {
		next, err := changedEnvironmentGitSource(source, update)
		if err != nil {
			return err
		}
		q := sqlc.New()
		row, err := q.UpdateEnvironmentGitSourceControl(ctx, tx, sqlc.UpdateEnvironmentGitSourceControlParams{SourceID: mustPgUUID(source.ID), ExpectedGeneration: source.Generation, Mode: next.Spec.Mode, Prune: next.Spec.Prune, Suspended: next.Suspended})
		if err != nil {
			return mapErr(err)
		}
		result = environmentGitSourceFromSQL(row, source.EnvironmentSlug)
		if result.ApprovedRevisionID != "" {
			if err := q.EnqueueEnvironmentGitOps(ctx, tx, sqlc.EnqueueEnvironmentGitOpsParams{SourceID: row.ID, Generation: row.Generation, NextAttemptAt: gitOpsTime(time.Now())}); err != nil {
				return err
			}
		}
		return recordGitOpsEvent(ctx, tx, sourceID, accountID, "control", update)
	})
	return result, err
}

func (s *PgStore) SetEnvironmentGitOpsOverride(ctx context.Context, accountID, sourceID string, request EnvironmentGitOpsOverrideRequest) error {
	if err := validateGitOpsOverride(request, time.Now()); err != nil {
		return err
	}
	return s.withGitOpsControl(ctx, accountID, sourceID, func(tx pgx.Tx, source EnvironmentGitSource) error {
		count, err := sqlc.New().PutEnvironmentGitOpsOverride(ctx, tx, sqlc.PutEnvironmentGitOpsOverrideParams{SourceID: mustPgUUID(sourceID), Actor: accountID, Resource: request.Resource, FieldPath: request.Path, Reason: request.Reason, ExpiresAt: gitOpsTime(request.ExpiresAt)})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrNotFound
		}
		if err := enqueueGitOpsControl(ctx, tx, source); err != nil {
			return err
		}
		return recordGitOpsEvent(ctx, tx, sourceID, accountID, "override_created", request)
	})
}

func (s *PgStore) RemoveEnvironmentGitOpsOverride(ctx context.Context, accountID, sourceID, resource, path string) error {
	return s.withGitOpsControl(ctx, accountID, sourceID, func(tx pgx.Tx, source EnvironmentGitSource) error {
		count, err := sqlc.New().DeleteEnvironmentGitOpsOverride(ctx, tx, sqlc.DeleteEnvironmentGitOpsOverrideParams{SourceID: mustPgUUID(sourceID), Resource: resource, FieldPath: path})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrNotFound
		}
		if err := enqueueGitOpsControl(ctx, tx, source); err != nil {
			return err
		}
		return recordGitOpsEvent(ctx, tx, sourceID, accountID, "override_removed", map[string]string{"resource": resource, "path": path})
	})
}
