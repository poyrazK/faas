package state

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func bindGitOpsQueueIdentity(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, change environmentsync.Change, id string) error {
	if id == "" {
		return ErrConflict
	}
	count, err := sqlc.New().BindEnvironmentGitOpsQueue(ctx, tx, sqlc.BindEnvironmentGitOpsQueueParams{
		SourceID: mustPgUUID(source.ID), Resource: change.Resource, FieldPath: change.Path, BindingID: mustPgUUID(id)})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) applyGitOpsQueue(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, definition api.EnvironmentDefinition, ids map[string]string, change environmentsync.Change) error {
	if err := gitOpsQueueMutationAllowed(change, definition); err != nil {
		return err
	}
	appID := ids[change.Resource]
	if appID == "" {
		return ErrConflict
	}
	id := ids[gitOpsQueueResource(change.Resource, change.Path)]
	var current sqlc.QueueBinding
	if id != "" {
		var err error
		current, err = sqlc.New().EnvironmentGitOpsQueueForUpdate(ctx, tx, sqlc.EnvironmentGitOpsQueueForUpdateParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID), AccountID: mustPgUUID(source.AccountID), EnvironmentID: mustPgUUID(source.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
	}
	if change.Action == "remove" {
		if id == "" {
			return ErrConflict
		}
		if current.RetiredAt.Valid {
			return nil
		}
		_, err := s.mutateQueueBindingConsumerTx(ctx, tx, source.AccountID, appID, id, nil, nil, true)
		return err
	}
	binding, err := decodeGitOpsQueue(change.Path, change.After, source.EnvironmentID, source.EnvironmentSlug, appID, source.AccountID)
	if err != nil {
		return err
	}
	if id == "" {
		if change.Action != "create" {
			return ErrConflict
		}
		binding.ID = uuid.NewString()
		_, err = s.mutateQueueBindingConsumerTx(ctx, tx, source.AccountID, appID, binding.ID, &binding, nil, false)
		id = binding.ID
	} else {
		if current.RetiredAt.Valid {
			if !gitOpsQueueRecoveryMatches(definition.Workloads[strings.TrimPrefix(change.Resource, "workload/")].QueueRecoveries[binding.Name], id) {
				return ErrConflict
			}
			if binding.Mode == "push" {
				account, err := sqlc.New().QueueConsumerLockAccount(ctx, tx, mustPgUUID(source.AccountID))
				if err != nil {
					return mapErr(err)
				}
				if err := queueConsumerCheckQuota(ctx, sqlc.New(), tx, mustPgUUID(appID), mustPgUUID(source.AccountID), api.MustLimitsFor(api.Plan(account.Plan))); err != nil {
					return err
				}
			}
			if _, err := sqlc.New().RecoverEnvironmentGitOpsQueue(ctx, tx, sqlc.RecoverEnvironmentGitOpsQueueParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID), AccountID: mustPgUUID(source.AccountID), EnvironmentID: mustPgUUID(source.EnvironmentID)}); err != nil {
				return mapErr(err)
			}
		}
		patch := gitOpsQueuePatch(binding)
		_, err = s.mutateQueueBindingConsumerTx(ctx, tx, source.AccountID, appID, id, nil, &patch, false)
	}
	if err != nil {
		return err
	}
	return bindGitOpsQueueIdentity(ctx, tx, source, change, id)
}
