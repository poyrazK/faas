package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func (s *PgStore) applyGitOpsQueue(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, ids map[string]string, change environmentsync.Change) error {
	if err := gitOpsQueueMutationAllowed(change); err != nil {
		return err
	}
	appID := ids[change.Resource]
	if appID == "" {
		return ErrConflict
	}
	binding, err := decodeGitOpsQueue(change.Path, change.After, source.EnvironmentID, source.EnvironmentSlug, appID, source.AccountID)
	if err != nil {
		return err
	}
	id := ids[gitOpsQueueResource(change.Resource, change.Path)]
	if id == "" {
		if change.Action != "create" {
			return ErrConflict
		}
		binding.ID = uuid.NewString()
		_, err = s.mutateQueueBindingConsumerTx(ctx, tx, source.AccountID, appID, binding.ID, &binding, nil, false)
		id = binding.ID
	} else {
		patch := gitOpsQueuePatch(binding)
		_, err = s.mutateQueueBindingConsumerTx(ctx, tx, source.AccountID, appID, id, nil, &patch, false)
	}
	if err != nil {
		return err
	}
	return bindGitOpsQueueIdentity(ctx, tx, source, change, id)
}
