package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CreateQueueBindingWithConsumer(ctx context.Context, in QueueBinding) (QueueBindingConsumerResult, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	return s.mutateQueueBindingConsumer(ctx, in.AccountID, in.AppID, in.ID, &in, nil, false)
}

func (s *PgStore) UpdateQueueBindingWithConsumer(ctx context.Context, accountID, appID, id string, p UpdateQueueBindingParams) (QueueBindingConsumerResult, error) {
	return s.mutateQueueBindingConsumer(ctx, accountID, appID, id, nil, &p, false)
}

func (s *PgStore) DeleteQueueBindingWithConsumer(ctx context.Context, accountID, appID, id string) (QueueBindingConsumerResult, error) {
	return s.mutateQueueBindingConsumer(ctx, accountID, appID, id, nil, nil, true)
}

func (s *PgStore) mutateQueueBindingConsumer(ctx context.Context, accountID, appID, id string, create *QueueBinding, patch *UpdateQueueBindingParams, remove bool) (QueueBindingConsumerResult, error) {
	var accountUUID, appUUID, bindingUUID pgtype.UUID
	for _, item := range []struct {
		value string
		out   *pgtype.UUID
	}{{accountID, &accountUUID}, {appID, &appUUID}, {id, &bindingUUID}} {
		parsed, err := uuid.Parse(item.value)
		if err != nil {
			return QueueBindingConsumerResult{}, ErrInvalidArgument
		}
		*item.out = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	accountID, appID, id = accountUUID.String(), appUUID.String(), bindingUUID.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return QueueBindingConsumerResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app, err := q.QueueConsumerLockApp(ctx, tx, sqlc.QueueConsumerLockAppParams{AppID: appUUID, AccountID: accountUUID})
	if err != nil {
		return QueueBindingConsumerResult{}, mapErr(err)
	}
	account, err := q.QueueConsumerLockAccount(ctx, tx, accountUUID)
	if err != nil {
		return QueueBindingConsumerResult{}, mapErr(err)
	}
	limits, _ := api.LimitsFor(api.Plan(account.Plan))
	var binding QueueBinding
	if create != nil {
		binding = applyQueueBindingPatch(*create, UpdateQueueBindingParams{})
		binding.ID, binding.AccountID, binding.AppID = id, accountID, appID
	} else {
		row, err := q.QueueConsumerBindingForUpdate(ctx, tx, sqlc.QueueConsumerBindingForUpdateParams{ID: bindingUUID, AppID: appUUID, AccountID: accountUUID})
		if err != nil {
			return QueueBindingConsumerResult{}, mapErr(err)
		}
		binding = queueConsumerBindingFromSQL(row)
		if patch != nil {
			binding = applyQueueBindingPatch(binding, *patch)
		}
	}
	if !remove {
		if err := validateQueueBindingConsumer(binding, AppType(app.Type), WorkloadClass(app.WorkloadClass)); err != nil {
			return QueueBindingConsumerResult{}, err
		}
	}
	owned, err := q.QueueConsumerOwnedTriggers(ctx, tx, sqlc.QueueConsumerOwnedTriggersParams{AppID: appUUID, BindingID: id})
	if err != nil {
		return QueueBindingConsumerResult{}, err
	}
	if len(owned) > 1 {
		return QueueBindingConsumerResult{}, fmt.Errorf("%w: duplicate queue consumer projection", ErrConflict)
	}
	var definition queueConsumerDefinition
	if !remove && binding.Mode == "push" {
		definition, err = queueConsumerForBinding(binding, limits)
		if err != nil {
			return QueueBindingConsumerResult{}, err
		}
		if len(owned) == 0 {
			if err := queueConsumerCheckQuota(ctx, q, tx, appUUID, accountUUID, limits); err != nil {
				return QueueBindingConsumerResult{}, err
			}
		}
	}
	if create != nil {
		row, err := q.QueueConsumerInsertBinding(ctx, tx, sqlc.QueueConsumerInsertBindingParams{ID: bindingUUID, AccountID: accountUUID, AppID: appUUID,
			Name: binding.Name, QueueName: binding.QueueName, Mode: binding.Mode, WorkloadClass: string(binding.WorkloadClass), Enabled: binding.Enabled,
			MaxConcurrency: int32(binding.MaxConcurrency), RetryPolicy: binding.RetryPolicyJSON})
		if err != nil {
			return QueueBindingConsumerResult{}, mapErr(err)
		}
		binding = queueConsumerBindingFromSQL(row)
	} else if !remove {
		row, err := q.QueueConsumerUpdateBinding(ctx, tx, sqlc.QueueConsumerUpdateBindingParams{ID: bindingUUID, AccountID: accountUUID, AppID: appUUID,
			QueueName: binding.QueueName, Mode: binding.Mode, WorkloadClass: string(binding.WorkloadClass), Enabled: binding.Enabled,
			MaxConcurrency: int32(binding.MaxConcurrency), RetryPolicy: binding.RetryPolicyJSON})
		if err != nil {
			return QueueBindingConsumerResult{}, mapErr(err)
		}
		binding = queueConsumerBindingFromSQL(row)
	}
	result := QueueBindingConsumerResult{Binding: binding}
	if remove || binding.Mode != "push" {
		if len(owned) == 1 {
			if err := q.DeleteTrigger(ctx, tx, sqlc.DeleteTriggerParams{ID: owned[0], AppID: appUUID}); err != nil {
				return QueueBindingConsumerResult{}, err
			}
			result.Changes = append(result.Changes, QueueConsumerChange{Kind: "deleted", AppID: appID, TriggerID: owned[0].String()})
		}
	} else if len(owned) == 1 {
		changed, err := q.QueueConsumerUpdateTrigger(ctx, tx, sqlc.QueueConsumerUpdateTriggerParams{ID: owned[0], AppID: appUUID,
			Slug: binding.QueueName, Enabled: binding.Enabled, Config: definition.Config, BatchSizeMax: definition.BatchSize,
			BatchWindowMs: definition.BatchWindow, MaxAttempts: definition.MaxAttempts, PayloadMaxBytes: definition.PayloadMax})
		if err != nil {
			return QueueBindingConsumerResult{}, mapErr(err)
		}
		if changed != 1 {
			return QueueBindingConsumerResult{}, ErrConflict
		}
		result.Changes = append(result.Changes, QueueConsumerChange{Kind: "updated", AppID: appID, TriggerID: owned[0].String()})
	} else {
		row, err := q.CreateTrigger(ctx, tx, sqlc.CreateTriggerParams{AccountID: accountUUID, AppID: appUUID, Kind: "queue", Slug: binding.QueueName,
			Enabled: binding.Enabled, Column6: definition.Config, BatchSizeMax: definition.BatchSize, BatchWindowMs: definition.BatchWindow,
			MaxAttempts: definition.MaxAttempts, PayloadMaxBytes: definition.PayloadMax, Source: nullableTriggerSource("queue"), BrokerPoisonStrategy: "commit"})
		if err != nil {
			return QueueBindingConsumerResult{}, mapErr(err)
		}
		result.Changes = append(result.Changes, QueueConsumerChange{Kind: "created", AppID: appID, TriggerID: row.ID.String()})
	}
	if remove {
		deleted, err := q.QueueConsumerDeleteBinding(ctx, tx, sqlc.QueueConsumerDeleteBindingParams{ID: bindingUUID, AppID: appUUID, AccountID: accountUUID})
		if err != nil {
			return QueueBindingConsumerResult{}, err
		}
		if deleted != 1 {
			return QueueBindingConsumerResult{}, ErrConflict
		}
	}
	for _, change := range result.Changes {
		payload, err := json.Marshal(change)
		if err != nil {
			return QueueBindingConsumerResult{}, err
		}
		if err := q.QueueConsumerNotify(ctx, tx, string(payload)); err != nil {
			return QueueBindingConsumerResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return QueueBindingConsumerResult{}, fmt.Errorf("state: commit queue binding consumer: %w", err)
	}
	result.NotificationsCommitted = true
	return result, nil
}

func queueConsumerCheckQuota(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, appID, accountID pgtype.UUID, limits api.Limits) error {
	count, err := q.CountTriggersByApp(ctx, tx, appID)
	if err != nil {
		return err
	}
	if count >= int64(limits.TriggerLimitPerApp) {
		return &TriggerQuotaError{Scope: TriggerQuotaScopeApp, Limit: limits.TriggerLimitPerApp, Observed: int(count)}
	}
	count, err = q.CountTriggersByAccount(ctx, tx, accountID)
	if err != nil {
		return err
	}
	if count >= int64(limits.TriggerLimitPerAccount) {
		return &TriggerQuotaError{Scope: TriggerQuotaScopeAccount, Limit: limits.TriggerLimitPerAccount, Observed: int(count)}
	}
	return nil
}

func queueConsumerBindingFromSQL(row sqlc.QueueBinding) QueueBinding {
	return QueueBinding{ID: row.ID.String(), AccountID: row.AccountID.String(), AppID: row.AppID.String(), Name: row.Name, QueueName: row.QueueName,
		Mode: row.Mode, WorkloadClass: WorkloadClass(row.WorkloadClass), Enabled: row.Enabled, MaxConcurrency: int(row.MaxConcurrency),
		RetryPolicyJSON: append([]byte(nil), row.RetryPolicy...), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

var _ QueueBindingConsumerStore = (*PgStore)(nil)
