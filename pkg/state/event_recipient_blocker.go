package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventRecipientBlockerStore interface {
	EventRecipientBlocker(context.Context, string, api.EventRecipientBlockerQuery) (api.EventRecipientBlockerResponse, error)
}

func validateEventRecipientBlocker(accountID string, query api.EventRecipientBlockerQuery) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return ErrInvalidArgument
	}
	if err := query.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return nil
}

func newEventRecipientBlocker(query api.EventRecipientBlockerQuery) api.EventRecipientBlockerResponse {
	return api.EventRecipientBlockerResponse{Query: query, ObservedAt: time.Now().UTC(), Status: "unavailable", Reason: "receipt_unavailable"}
}

func recipientBlockerTarget(out *api.EventRecipientBlockerResponse, acceptedAt time.Time, captured, found bool, appID, routingState string) bool {
	switch {
	case !acceptedAt.Equal(out.Query.ExpectedAcceptedAt):
		out.Status, out.Reason = "acceptance_replaced", "acceptance_replaced"
	case !captured:
		out.Reason = "snapshot_unavailable"
	case !found || appID == "":
		out.Reason = "recipient_unavailable"
	default:
		out.RoutingState = routingState
		out.Reason = "routing_evidence_unavailable"
		return true
	}
	return false
}

func (s *PgStore) EventRecipientBlocker(ctx context.Context, accountID string, query api.EventRecipientBlockerQuery) (api.EventRecipientBlockerResponse, error) {
	out := newEventRecipientBlocker(query)
	if err := validateEventRecipientBlocker(accountID, query); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin recipient blocker read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	target, err := q.EventRecipientBlockerTarget(ctx, tx, sqlc.EventRecipientBlockerTargetParams{AccountID: mustPgUUID(accountID), EventSource: query.Source, EventID: query.ID, SubscriptionID: query.SubscriptionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read recipient blocker target: %w", err)
	}
	if !recipientBlockerTarget(&out, timeFromPgtype(target.CreatedAt), target.SnapshotCaptured, target.RecipientFound, target.AppID, target.RoutingState) {
		return out, nil
	}
	rows, err := q.EventRecipientBlockerWaiting(ctx, tx, sqlc.EventRecipientBlockerWaitingParams{AccountID: mustPgUUID(accountID), OutboxID: target.ID, SubscriptionID: query.SubscriptionID, ObservedAt: nullableTimestamptz(out.ObservedAt)})
	if err != nil {
		return out, fmt.Errorf("read recipient blocker: %w", err)
	}
	if len(rows) == 1 && (target.RoutingState == "pending" || target.RoutingState == "processing") {
		row := rows[0]
		recipient := api.EventBacklogRecipient{DeliveryControlReason: row.DeliveryControlReason, EventSource: row.Source, EventID: row.EventID, EventType: row.EventType, AcceptedAt: timeFromPgtype(row.AcceptedAt), AppID: uuidFromPgtype(row.AppID).String(), AppSlug: row.AppSlug, TargetAvailable: row.TargetAvailable, SubscriptionID: row.SubscriptionID, ConsumerKind: row.ConsumerKind, Origin: row.Origin, WorkflowName: row.WorkflowName, RoutingMode: row.RoutingMode, State: row.RoutingState, CapacityScope: row.CapacityScope, Attempts: int(row.Attempts), CapacityDeferrals: int(row.CapacityDeferrals), NextAttemptAt: timestamptzToTimePtr(row.NextAttemptAt), LeaseUntil: timestamptzToTimePtr(row.LeaseUntil)}
		if len(row.OrderingBlocker) != 0 && string(row.OrderingBlocker) != "null" {
			if err := json.Unmarshal(row.OrderingBlocker, &recipient.OrderingBlocker); err != nil {
				return out, fmt.Errorf("decode ordering blocker: %w", err)
			}
		}
		if recipient.State == target.RoutingState && recipient.AcceptedAt.Equal(query.ExpectedAcceptedAt) && sameMemUUID(recipient.AppID, target.AppID) {
			recipientBlockerObservation(&recipient, out.ObservedAt)
			out.Status, out.Reason, out.Recipient = "available", "", &recipient
		}
	} else if len(rows) == 0 && recipientRoutingSettled(target.RoutingState) {
		out.Status, out.Reason = "not_waiting", ""
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit recipient blocker read: %w", err)
	}
	return out, nil
}

func recipientRoutingSettled(state string) bool {
	switch state {
	case "enqueued", "failed", "filtered":
		return true
	default:
		return false
	}
}

func (m *MemStore) EventRecipientBlocker(ctx context.Context, accountID string, query api.EventRecipientBlockerQuery) (api.EventRecipientBlockerResponse, error) {
	out := newEventRecipientBlocker(query)
	if err := validateEventRecipientBlocker(accountID, query); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+query.Source+"\x00"+query.ID]
	if work == nil {
		return out, nil
	}
	var identity publishedEventIdentity
	if err := json.Unmarshal(work.Payload, &identity); err != nil {
		return out, fmt.Errorf("decode recipient blocker identity: %w", err)
	}
	index := -1
	for i, recipient := range work.RecipientSnapshot {
		if recipient.ID == query.SubscriptionID {
			index = i
			break
		}
	}
	if index < 0 {
		recipientBlockerTarget(&out, work.CreatedAt, work.SnapshotCaptured, false, "", "")
		return out, nil
	}
	recipient := work.RecipientSnapshot[index]
	entry := m.backlogRecipientLocked(accountID, work, recipient, identity)
	if !recipientBlockerTarget(&out, work.CreatedAt, work.SnapshotCaptured, true, recipient.AppID, entry.State) {
		return out, nil
	}
	if recipientRoutingSettled(entry.State) {
		out.Status, out.Reason = "not_waiting", ""
		return out, nil
	}
	if work.Delivered || (entry.State != "pending" && entry.State != "processing") {
		return out, nil
	}
	if entry.State == "pending" {
		if prior, position := m.orderedEventRecipientBlockerLocked(work, index, work.RecipientClaims); prior != nil {
			var priorIdentity publishedEventIdentity
			if err := json.Unmarshal(prior.Payload, &priorIdentity); err != nil {
				return out, fmt.Errorf("decode prior recipient identity: %w", err)
			}
			blocked := m.backlogRecipientLocked(accountID, prior, prior.RecipientSnapshot[position], priorIdentity)
			entry.OrderingBlocker = &api.EventOrderingBlocker{EventSource: blocked.EventSource, EventID: blocked.EventID, SubscriptionID: blocked.SubscriptionID, AcceptedAt: blocked.AcceptedAt, State: blocked.State, NextAttemptAt: blocked.NextAttemptAt}
		}
	}
	entry.DeliveryControlReason = m.eventSubscriptionWaitingReasonLocked(accountID, recipient.AppID, recipient.ID, out.ObservedAt)
	recipientBlockerObservation(&entry.EventBacklogRecipient, out.ObservedAt)
	out.Status, out.Reason, out.Recipient = "available", "", &entry.EventBacklogRecipient
	return out, nil
}

// An active shared lease is current routing work, ahead of pending admission blockers.
func recipientBlockerObservation(recipient *api.EventBacklogRecipient, observedAt time.Time) {
	backlogObservation(recipient, observedAt)
	if recipient.State == "pending" && recipient.RoutingMode == "event" && recipient.LeaseUntil != nil && recipient.LeaseUntil.After(observedAt) {
		recipient.WaitingReason = "receipt_processing"
	}
}
