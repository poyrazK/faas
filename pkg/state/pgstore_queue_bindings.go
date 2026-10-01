package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func queueBindingIdentity(accountID, appID, id string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	var ids [3]pgtype.UUID
	for i, value := range []string{accountID, appID, id} {
		if value == "" && i == 2 {
			continue
		}
		parsed, err := uuid.Parse(value)
		if err != nil {
			return ids[0], ids[1], ids[2], ErrInvalidArgument
		}
		ids[i] = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	return ids[0], ids[1], ids[2], nil
}

// CreateQueueBinding is the legacy intent-only seam. Consumer publication uses
// CreateQueueBindingWithConsumer; retirement always uses its atomic counterpart.
func (s *PgStore) CreateQueueBinding(ctx context.Context, in QueueBinding) (QueueBinding, error) {
	if in.EnvironmentID != "" {
		if _, err := uuid.Parse(in.EnvironmentID); err != nil {
			return QueueBinding{}, ErrInvalidArgument
		}
	}
	if in.RetiredAt != nil {
		return QueueBinding{}, ErrInvalidArgument
	}
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	account, app, id, err := queueBindingIdentity(in.AccountID, in.AppID, in.ID)
	if err != nil {
		return QueueBinding{}, err
	}
	in = applyQueueBindingPatch(in, UpdateQueueBindingParams{})
	row, err := sqlc.New().QueueConsumerInsertBinding(ctx, s.pool, sqlc.QueueConsumerInsertBindingParams{
		ID: id, AccountID: account, AppID: app, Name: in.Name, QueueName: in.QueueName,
		Mode: in.Mode, WorkloadClass: string(in.WorkloadClass), Enabled: in.Enabled,
		MaxConcurrency: int32(in.MaxConcurrency), RetryPolicy: in.RetryPolicyJSON, DeploymentScope: in.DeploymentScope, EnvironmentID: mustPgUUID(in.EnvironmentID),
	})
	if err != nil {
		return QueueBinding{}, fmt.Errorf("state: insert queue binding: %w", mapErr(err))
	}
	return queueConsumerBindingFromSQL(row), nil
}

func (s *PgStore) QueueBindingByID(ctx context.Context, accountID, appID, id string) (QueueBinding, error) {
	row, err := s.QueueBindingHistoryByID(ctx, accountID, appID, id)
	if err == nil && row.RetiredAt != nil {
		return QueueBinding{}, ErrNotFound
	}
	return row, err
}

func (s *PgStore) QueueBindingHistoryByID(ctx context.Context, accountID, appID, id string) (QueueBinding, error) {
	account, app, binding, err := queueBindingIdentity(accountID, appID, id)
	if err != nil {
		return QueueBinding{}, err
	}
	row, err := sqlc.New().QueueBindingHistoryByID(ctx, s.pool, sqlc.QueueBindingHistoryByIDParams{ID: binding, AccountID: account, AppID: app})
	if err != nil {
		return QueueBinding{}, fmt.Errorf("state: read queue binding: %w", mapErr(err))
	}
	return queueConsumerBindingFromSQL(row), nil
}

func (s *PgStore) ListQueueBindingHistoryForApp(ctx context.Context, accountID, appID string) ([]QueueBinding, error) {
	account, app, _, err := queueBindingIdentity(accountID, appID, "")
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListQueueBindingHistoryForApp(ctx, s.pool, sqlc.ListQueueBindingHistoryForAppParams{AccountID: account, AppID: app})
	if err != nil {
		return nil, fmt.Errorf("state: list queue bindings: %w", mapErr(err))
	}
	out := make([]QueueBinding, 0, len(rows))
	for _, row := range rows {
		out = append(out, queueConsumerBindingFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]QueueBinding, error) {
	rows, err := s.ListQueueBindingHistoryForApp(ctx, accountID, appID)
	if err != nil {
		return nil, err
	}
	out := make([]QueueBinding, 0, len(rows))
	for _, row := range rows {
		if row.RetiredAt == nil {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *PgStore) UpdateQueueBinding(ctx context.Context, accountID, appID, id string, p UpdateQueueBindingParams) (QueueBinding, error) {
	account, app, binding, err := queueBindingIdentity(accountID, appID, id)
	if err != nil {
		return QueueBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return QueueBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.QueueConsumerBindingForUpdate(ctx, tx, sqlc.QueueConsumerBindingForUpdateParams{ID: binding, AccountID: account, AppID: app})
	if err != nil {
		return QueueBinding{}, mapErr(err)
	}
	// Private consumers must be updated in the atomic publication transaction.
	owned, err := q.QueueConsumerOwnedTriggers(ctx, tx, sqlc.QueueConsumerOwnedTriggersParams{AppID: app, BindingID: binding})
	if err != nil {
		return QueueBinding{}, err
	}
	if len(owned) != 0 {
		return QueueBinding{}, ErrConflict
	}
	current := applyQueueBindingPatch(queueConsumerBindingFromSQL(row), p)
	row, err = q.QueueConsumerUpdateBinding(ctx, tx, sqlc.QueueConsumerUpdateBindingParams{ID: binding, AccountID: account, AppID: app,
		QueueName: current.QueueName, Mode: current.Mode, WorkloadClass: string(current.WorkloadClass),
		Enabled: current.Enabled, MaxConcurrency: int32(current.MaxConcurrency), RetryPolicy: current.RetryPolicyJSON})
	if err != nil {
		return QueueBinding{}, fmt.Errorf("state: update queue binding: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return QueueBinding{}, err
	}
	return queueConsumerBindingFromSQL(row), nil
}

func (s *PgStore) DeleteQueueBinding(ctx context.Context, accountID, appID, id string) error {
	_, err := s.DeleteQueueBindingWithConsumer(ctx, accountID, appID, id)
	return err
}

var _ QueueBindingHistoryStore = (*PgStore)(nil)
