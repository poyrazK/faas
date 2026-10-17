package internalmail

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) publishEventFeed(ctx context.Context, enabled func() bool) (int, error) {
	if enabled == nil || !enabled() {
		return 0, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.queries.LockEventPublication(ctx, tx, s.namespace); err != nil {
		return 0, err
	}
	rows, err := s.queries.UnpublishedDeliveryEvents(ctx, tx, q.UnpublishedDeliveryEventsParams{Namespace: s.namespace, Limit: api.InternalMailEventFeedBatchSize})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		position, err := s.queries.NextEventPosition(ctx, tx, q.NextEventPositionParams{Namespace: s.namespace, ProducerID: r.ProducerID})
		if err != nil {
			return 0, err
		}
		if err := s.queries.PublishDeliveryEvent(ctx, tx, q.PublishDeliveryEventParams{Namespace: s.namespace, ProducerID: r.ProducerID, Position: position, ID: pgID(uuid.NewString()), SourceEventID: r.SourceEventID, MessageID: r.MessageID, RecipientID: r.RecipientID, DomainID: r.DomainID, Kind: r.Kind, OccurredAt: r.CreatedAt}); err != nil {
			return 0, err
		}
	}
	if !enabled() {
		return 0, ErrDisabled
	}
	return len(rows), tx.Commit(ctx)
}

func (s *PGStore) lockEventConsumers(ctx context.Context, tx pgx.Tx, producer string) error {
	return s.queries.LockEventConsumers(ctx, tx, q.LockEventConsumersParams{Namespace: s.namespace, ProducerID: producer})
}
func (s *PGStore) loadEventConsumer(ctx context.Context, tx pgx.Tx, producer, consumer string, create bool) (q.InternalMailEventConsumer, error) {
	r, err := s.queries.EventConsumer(ctx, tx, q.EventConsumerParams{Namespace: s.namespace, ProducerID: producer, Name: consumer})
	if !create || !errors.Is(err, pgx.ErrNoRows) {
		return r, storeError(err)
	}
	n, err := s.queries.EventConsumerCount(ctx, tx, q.EventConsumerCountParams{Namespace: s.namespace, ProducerID: producer})
	if err != nil {
		return r, err
	}
	if n >= api.InternalMailEventFeedMaxConsumers {
		return r, ErrQuota
	}
	r, err = s.queries.CreateEventConsumer(ctx, tx, q.CreateEventConsumerParams{Namespace: s.namespace, ProducerID: producer, Name: consumer})
	return r, err
}

func (s *PGStore) pullEventFeed(ctx context.Context, producer, consumer string, limit int, enabled func() bool) (EventFeedBatch, error) {
	if enabled == nil || !enabled() {
		return EventFeedBatch{}, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EventFeedBatch{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.shareRecovery(ctx, tx, ""); err != nil {
		return EventFeedBatch{}, err
	}
	if err := s.shareProducerAccess(ctx, tx, producer); err != nil {
		return EventFeedBatch{}, err
	}
	if err := s.lockEventConsumers(ctx, tx, producer); err != nil {
		return EventFeedBatch{}, err
	}
	c, err := s.loadEventConsumer(ctx, tx, producer, consumer, true)
	if err != nil {
		return EventFeedBatch{}, err
	}
	batch, err := s.prepareEventBatch(ctx, tx, c, limit)
	if err != nil {
		return EventFeedBatch{}, err
	}
	if err := s.queries.SetPendingEventReceipt(ctx, tx, q.SetPendingEventReceiptParams{Namespace: s.namespace, ProducerID: producer, Name: consumer, PendingReceipt: pgID(batch.Receipt)}); err != nil {
		return EventFeedBatch{}, err
	}
	if !enabled() {
		return EventFeedBatch{}, ErrDisabled
	}
	if err := s.recheckProducerCredential(ctx, tx, producer); err != nil {
		return EventFeedBatch{}, err
	}
	return batch, tx.Commit(ctx)
}

func (s *PGStore) prepareEventBatch(ctx context.Context, tx pgx.Tx, c q.InternalMailEventConsumer, limit int) (EventFeedBatch, error) {
	batch := EventFeedBatch{ProducerID: c.ProducerID, Consumer: c.Name, Generation: c.Generation, Events: []DeliveryEvent{}}
	if c.Checkpoint > 0 {
		r, err := s.queries.DeliveryEventAt(ctx, tx, q.DeliveryEventAtParams{Namespace: s.namespace, ProducerID: c.ProducerID, Position: c.Checkpoint})
		if err != nil {
			return batch, storeError(err)
		}
		batch.Cursor = idString(r.ID)
	}
	batch.NextCursor = batch.Cursor
	through := int64(0)
	if c.PendingReceipt.Valid {
		r, err := s.queries.EventReceipt(ctx, tx, q.EventReceiptParams{Namespace: s.namespace, ProducerID: c.ProducerID, Consumer: c.Name, ID: c.PendingReceipt})
		if err != nil {
			return batch, storeError(err)
		}
		if r.Generation != c.Generation || r.FromPosition != c.Checkpoint || r.AcknowledgedAt.Valid {
			return batch, ErrConflict
		}
		through = r.ToPosition
		batch.Receipt = idString(r.ID)
		limit = api.InternalMailEventFeedMaxPageSize
	}
	rows, err := s.queries.DeliveryEventBatch(ctx, tx, q.DeliveryEventBatchParams{Namespace: s.namespace, ProducerID: c.ProducerID, AfterPosition: c.Checkpoint, ThroughPosition: through, PageLimit: int32(limit)})
	if err != nil {
		return batch, err
	}
	previous := c.Checkpoint
	for _, r := range rows {
		if r.Position != previous+1 {
			return batch, ErrConflict
		}
		batch.Events = append(batch.Events, deliveryEventRow(r))
		previous = r.Position
	}
	if len(rows) == 0 {
		if through > 0 {
			return batch, ErrConflict
		}
		return batch, nil
	}
	last := rows[len(rows)-1]
	batch.NextCursor = idString(last.ID)
	if through > 0 {
		if last.Position != through {
			return batch, ErrConflict
		}
		return batch, nil
	}
	r, err := s.queries.CreateEventReceipt(ctx, tx, q.CreateEventReceiptParams{Namespace: s.namespace, ProducerID: c.ProducerID, Consumer: c.Name, ID: pgID(uuid.NewString()), Generation: c.Generation, FromPosition: c.Checkpoint, ToPosition: last.Position})
	batch.Receipt = idString(r.ID)
	return batch, err
}

func deliveryEventRow(r q.InternalMailEventFeed) DeliveryEvent {
	return DeliveryEvent{ID: idString(r.ID), Sequence: r.Position, MessageID: idString(r.MessageID), RecipientID: idString(r.RecipientID), DomainID: idString(r.DomainID), Kind: r.Kind, OccurredAt: r.OccurredAt.Time, PublishedAt: r.PublishedAt.Time}
}

func (s *PGStore) ackEventFeed(ctx context.Context, producer, consumer string, input EventFeedAckInput, enabled func() bool) (EventFeedAck, error) {
	if enabled == nil || !enabled() {
		return EventFeedAck{}, ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EventFeedAck{}, err
	}
	defer rollbackOperator(ctx, tx)
	if err := s.shareRecovery(ctx, tx, ""); err != nil {
		return EventFeedAck{}, err
	}
	if err := s.shareProducerAccess(ctx, tx, producer); err != nil {
		return EventFeedAck{}, err
	}
	if err := s.lockEventConsumers(ctx, tx, producer); err != nil {
		return EventFeedAck{}, err
	}
	c, err := s.loadEventConsumer(ctx, tx, producer, consumer, false)
	if err != nil {
		return EventFeedAck{}, err
	}
	if input.Generation != c.Generation {
		return EventFeedAck{}, ErrEventFeedGeneration
	}
	r, err := s.queries.EventReceipt(ctx, tx, q.EventReceiptParams{Namespace: s.namespace, ProducerID: producer, Consumer: consumer, ID: pgID(input.Receipt)})
	if err != nil {
		return EventFeedAck{}, storeError(err)
	}
	if r.Generation != c.Generation {
		return EventFeedAck{}, ErrEventFeedGeneration
	}
	last, err := s.queries.DeliveryEventAt(ctx, tx, q.DeliveryEventAtParams{Namespace: s.namespace, ProducerID: producer, Position: r.ToPosition})
	if err != nil {
		return EventFeedAck{}, storeError(err)
	}
	result := EventFeedAck{ProducerID: producer, Consumer: consumer, Generation: c.Generation, Receipt: input.Receipt, Cursor: idString(last.ID)}
	if !r.AcknowledgedAt.Valid {
		if idString(c.PendingReceipt) != input.Receipt || c.Checkpoint != r.FromPosition {
			return EventFeedAck{}, ErrConflict
		}
		if err := s.queries.AcknowledgeEventReceipt(ctx, tx, q.AcknowledgeEventReceiptParams{Namespace: s.namespace, ProducerID: producer, Consumer: consumer, ID: r.ID}); err != nil {
			return EventFeedAck{}, err
		}
		if err := s.queries.AdvanceEventConsumer(ctx, tx, q.AdvanceEventConsumerParams{Namespace: s.namespace, ProducerID: producer, Name: consumer, Checkpoint: r.ToPosition}); err != nil {
			return EventFeedAck{}, err
		}
	}
	if !enabled() {
		return EventFeedAck{}, ErrDisabled
	}
	if err := s.recheckProducerCredential(ctx, tx, producer); err != nil {
		return EventFeedAck{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *PGStore) consumerStatus(ctx context.Context, db q.DBTX, producer, consumer string) (EventConsumerStatus, error) {
	r, err := s.queries.EventConsumerStatus(ctx, db, q.EventConsumerStatusParams{Namespace: s.namespace, ProducerID: producer, Name: consumer})
	cursor := idString(r.Cursor)
	if cursor == uuid.Nil.String() {
		cursor = ""
	}
	return EventConsumerStatus{ProducerID: r.ProducerID, Consumer: r.Name, Generation: r.Generation, Cursor: cursor, PendingReceipt: idString(r.PendingReceipt), Backlog: r.Backlog, OldestSeconds: r.OldestSeconds, CreatedAt: r.CreatedAt.Time, LastPullAt: r.LastPullAt.Time, LastAckAt: timePointer(r.LastAckAt)}, storeError(err)
}
func (s *PGStore) eventConsumerStatus(ctx context.Context, producer, consumer string) (EventConsumerStatus, error) {
	return s.consumerStatus(ctx, s.pool, producer, consumer)
}
func (s *PGStore) eventConsumers(ctx context.Context, producer string, list OperatorList) (OperatorPage[EventConsumerStatus], error) {
	if !validProducerID(producer) || list.validate(false) != nil || list.After != "" && !validEventConsumer(list.After) {
		return OperatorPage[EventConsumerStatus]{}, ErrInvalid
	}
	rows, err := s.queries.EventConsumerNames(ctx, s.pool, q.EventConsumerNamesParams{Namespace: s.namespace, ProducerID: producer, Name: list.After, Limit: int32(list.Limit + 1)})
	if err != nil {
		return OperatorPage[EventConsumerStatus]{}, err
	}
	items := make([]EventConsumerStatus, 0, len(rows))
	for _, name := range rows {
		status, err := s.eventConsumerStatus(ctx, producer, name)
		if err != nil {
			return OperatorPage[EventConsumerStatus]{}, err
		}
		items = append(items, status)
	}
	return operatorPage(items, list.Limit, func(s EventConsumerStatus) string { return s.Consumer }), nil
}

func (s *PGStore) replayEventConsumer(ctx context.Context, input EventFeedReplayRequest, enabled func() bool) (OperationRecord, error) {
	return s.operatorMutation(ctx, input.OperatorAuditInput, "event_feed_replay", input.ProducerID, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.lockEventConsumers(ctx, tx, input.ProducerID); err != nil {
			return nil, err
		}
		c, err := s.loadEventConsumer(ctx, tx, input.ProducerID, input.Consumer, false)
		if err != nil {
			return nil, err
		}
		if c.Generation != *input.ExpectedGeneration {
			return nil, ErrEventFeedGeneration
		}
		position := int64(0)
		if input.Cursor != "" {
			position, err = s.queries.DeliveryEventCursor(ctx, tx, q.DeliveryEventCursorParams{Namespace: s.namespace, ProducerID: input.ProducerID, ID: pgID(input.Cursor)})
			if err != nil {
				return nil, storeError(err)
			}
		}
		if position > c.Checkpoint {
			return nil, ErrInvalid
		}
		if err := s.queries.ReplayEventConsumer(ctx, tx, q.ReplayEventConsumerParams{Namespace: s.namespace, ProducerID: input.ProducerID, Name: input.Consumer, Checkpoint: position}); err != nil {
			return nil, err
		}
		return s.consumerStatus(ctx, tx, input.ProducerID, input.Consumer)
	})
}
