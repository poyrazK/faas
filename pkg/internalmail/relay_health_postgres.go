package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func relayStatusRow(r q.InternalMailRelayHealth) RelayStatus {
	s := RelayStatus{State: r.State, Reason: r.Reason, Revision: r.Revision, ReceivedAt: timePointer(r.ReceivedAt), HealthySince: timePointer(r.HealthySince), UpdatedAt: r.UpdatedAt.Time}
	if r.ObservationID.Valid {
		s.Observation = &RelayObservation{ID: idString(r.ObservationID), ObservedAt: r.ObservedAt.Time, InventoryOK: r.InventoryOk, SMTPUp: r.SmtpUp, QueueMessages: r.QueueMessages, OldestSeconds: r.OldestSeconds}
	}
	return s
}

func (s *PGStore) observeRelay(ctx context.Context, input RelayObservation, enabled func() bool) (RelayStatus, error) {
	if enabled == nil || !enabled() {
		return RelayStatus{}, ErrDisabled
	}
	input, err := input.normalized()
	if err != nil {
		return RelayStatus{}, err
	}
	return s.mutateRelay(ctx, &input, enabled)
}

func (s *PGStore) refreshRelay(ctx context.Context) (RelayStatus, error) {
	return s.mutateRelay(ctx, nil, func() bool { return true })
}

func (s *PGStore) mutateRelay(ctx context.Context, input *RelayObservation, enabled func() bool) (RelayStatus, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RelayStatus{}, err
	}
	defer rollbackOperator(ctx, tx)
	// The same namespace lock fences observations, manual namespace holds and
	// every first handoff, including when the health row does not yet exist.
	if err := s.queries.LockDeliveryScope(ctx, tx, q.LockDeliveryScopeParams{Namespace: s.namespace, ScopeID: pgID(operatorAfterID(""))}); err != nil {
		return RelayStatus{}, err
	}
	if err := s.queries.EnsureRelayHealth(ctx, tx, s.namespace); err != nil {
		return RelayStatus{}, err
	}
	r, err := s.queries.LockRelayHealth(ctx, tx, s.namespace)
	if err != nil {
		return RelayStatus{}, err
	}
	clock, err := s.queries.RelayDatabaseTime(ctx, tx)
	if err != nil {
		return RelayStatus{}, err
	}
	previous := relayStatusRow(r)
	next, err := applyRelayInput(previous, input, clock.Time)
	if err != nil {
		return RelayStatus{}, err
	}
	changed := previous.Revision == 0 || next.State != previous.State || next.Reason != previous.Reason
	if changed {
		next.Revision++
		next.UpdatedAt = clock.Time
		id, err := uuid.NewV7()
		if err != nil {
			return RelayStatus{}, err
		}
		observationID := ""
		if next.Observation != nil {
			observationID = next.Observation.ID
		}
		if err := s.queries.InsertRelayEvent(ctx, tx, q.InsertRelayEventParams{Namespace: s.namespace, ID: pgID(id.String()), Revision: next.Revision, State: next.State, Reason: next.Reason, ObservationID: pgID(observationID)}); err != nil {
			return RelayStatus{}, err
		}
	}
	if changed || input != nil && (previous.Observation == nil || input.ID != previous.Observation.ID) {
		if err := s.saveRelay(ctx, tx, next); err != nil {
			return RelayStatus{}, err
		}
	}
	if !enabled() {
		return RelayStatus{}, ErrDisabled
	}
	return next, tx.Commit(ctx)
}

func applyRelayInput(previous RelayStatus, input *RelayObservation, now time.Time) (RelayStatus, error) {
	if input == nil {
		return previous.view(now), nil
	}
	if previous.Observation != nil {
		if input.ID == previous.Observation.ID {
			if !sameRelayObservation(*input, *previous.Observation) {
				return RelayStatus{}, ErrConflict
			}
			return previous.view(now), nil
		}
		if !input.ObservedAt.After(previous.Observation.ObservedAt) {
			return RelayStatus{}, ErrConflict
		}
	}
	if !relayFresh(input.ObservedAt, now, now) {
		return RelayStatus{}, ErrInvalid
	}
	return nextRelayObservation(previous, *input, now), nil
}

func (s *PGStore) saveRelay(ctx context.Context, db q.DBTX, status RelayStatus) error {
	r := q.SaveRelayHealthParams{Namespace: s.namespace, State: status.State, Reason: status.Reason, Revision: status.Revision, ReceivedAt: sqlTime(status.ReceivedAt), HealthySince: sqlTime(status.HealthySince), UpdatedAt: sqlTime(&status.UpdatedAt)}
	if o := status.Observation; o != nil {
		r.ObservationID, r.ObservedAt = pgID(o.ID), sqlTime(&o.ObservedAt)
		r.InventoryOk, r.SmtpUp, r.QueueMessages, r.OldestSeconds = o.InventoryOK, o.SMTPUp, o.QueueMessages, o.OldestSeconds
	}
	return s.queries.SaveRelayHealth(ctx, db, r)
}

func (s *PGStore) relayStatus(ctx context.Context) (RelayStatus, error) {
	r, err := s.queries.RelayHealth(ctx, s.pool, s.namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		return (RelayStatus{}).view(time.Now()), nil
	}
	if err != nil {
		return RelayStatus{}, err
	}
	clock, err := s.queries.RelayDatabaseTime(ctx, s.pool)
	return relayStatusRow(r).view(clock.Time), err
}

func (s *PGStore) relayEvents(ctx context.Context, list OperatorList) (OperatorPage[RelayEvent], error) {
	if err := list.validate(true); err != nil {
		return OperatorPage[RelayEvent]{}, err
	}
	rows, err := s.queries.RelayEvents(ctx, s.pool, q.RelayEventsParams{Namespace: s.namespace, AfterID: pgID(operatorAfterID(list.After)), PageLimit: int32(list.Limit + 1)})
	items := make([]RelayEvent, 0, len(rows))
	for _, r := range rows {
		items = append(items, RelayEvent{ID: idString(r.ID), Revision: r.Revision, State: r.State, Reason: r.Reason, ObservationID: idString(r.ObservationID), CreatedAt: r.CreatedAt.Time})
	}
	return operatorPage(items, list.Limit, func(e RelayEvent) string { return e.ID }), err
}

func (s *PGStore) checkRelayDispatch(ctx context.Context, db q.DBTX) error {
	allowed, err := s.queries.RelayDispatchPermitted(ctx, db, q.RelayDispatchPermittedParams{Namespace: s.namespace, MaxSampleAgeSeconds: int32(api.InternalMailRelayMaxSampleAge / time.Second), ClockSkewSeconds: int32(api.InternalMailRelayClockSkew / time.Second), HighWater: api.InternalMailRelayQueueHighWater, MaxQueueAgeSeconds: int32(api.InternalMailRelayQueueMaxAge / time.Second)})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRelayHeld
	}
	return nil
}
