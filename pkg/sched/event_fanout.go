package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventInvocationMethod = "POST"
const eventInvocationPath = "/"
const eventFanoutRecoveryWindow = 10 * time.Minute
const eventFanoutRecoveryBatch = 1000
const eventFanoutSubscriptionBatch = 256

// routePublishedEvent is the schedd-side fanout seam for the internal event
// fabric. The publish endpoint persists the canonical envelope before sending
// its advisory wake; this worker matches only subscriptions owned by the same
// account and turns each match into an ordinary async invocation. The
// invocation ID is deterministic, so reconnects or duplicate LISTEN delivery
// cannot enqueue a second invocation for the same event/subscription pair.
func (l *Loop) routePublishedEvent(ctx context.Context, payload string) error {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return nil
	}
	var envelope events.Envelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return fmt.Errorf("sched: decode event.published payload: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return fmt.Errorf("sched: validate event.published payload: %w", err)
	}
	store, ok := l.engine.store.(state.EventSubscriptionMatcherStore)
	if !ok {
		// Older test stores and mixed-version boxes have no bounded candidate
		// reader yet. The persisted event remains available for a later recovery
		// sweep, so this notification is intentionally a no-op.
		return nil
	}
	eventPayload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("sched: encode event invocation payload: %w", err)
	}
	now := time.Now().UTC()
	var routeErrs []error
	cursor := state.EventSubscriptionCursor{}
	for {
		subscriptions, listErr := store.ListMatchingEventSubscriptionsForAccount(ctx, envelope.AccountID, envelope.Source, envelope.Type, cursor, eventFanoutSubscriptionBatch)
		if listErr != nil {
			return fmt.Errorf("sched: list matching event subscriptions: %w", listErr)
		}
		for _, row := range subscriptions {
			matched, matchErr := (events.Subscription{
				ID:        row.ID,
				AccountID: row.AccountID,
				Source:    row.Source,
				Type:      row.Type,
				Filter:    row.Filter,
			}).Match(envelope)
			if matchErr != nil {
				routeErrs = append(routeErrs, fmt.Errorf("subscription %s: %w", row.ID, matchErr))
				continue
			}
			if !matched {
				continue
			}
			invocationID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:event:"+envelope.ID+"\x00"+row.ID)).String()
			headers, marshalErr := json.Marshal(map[string]string{
				"x-gregale-event-id":     envelope.ID,
				"x-gregale-event-source": envelope.Source,
				"x-gregale-event-type":   envelope.Type,
			})
			if marshalErr != nil {
				routeErrs = append(routeErrs, marshalErr)
				continue
			}
			_, enqueueErr := l.engine.store.EnqueueInvocation(ctx, state.Invocation{
				ID:        invocationID,
				AppID:     row.AppID,
				AccountID: row.AccountID,
				Source:    state.InvocationAsyncInvoke,
				State:     state.InvocationPending,
				Method:    eventInvocationMethod,
				Path:      eventInvocationPath,
				Payload:   eventPayload,
				Headers:   headers,
				DueAt:     now,
				CreatedAt: now,
			})
			if enqueueErr != nil && !errors.Is(enqueueErr, state.ErrConflict) {
				routeErrs = append(routeErrs, fmt.Errorf("subscription %s: enqueue invocation: %w", row.ID, enqueueErr))
				continue
			}
			if enqueueErr == nil && l.pool != nil {
				_ = db.Notify(ctx, l.pool, db.NotifyInvocationDue,
					fmt.Sprintf(`{"invocation_id":"%s","app_id":"%s","source":"%s"}`, invocationID, row.AppID, state.InvocationAsyncInvoke))
			}
		}
		if len(subscriptions) < eventFanoutSubscriptionBatch {
			break
		}
		last := subscriptions[len(subscriptions)-1]
		cursor = state.EventSubscriptionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return errors.Join(routeErrs...)
}

// runEventFanoutSweep closes the LISTEN loss window. Published envelopes are
// retained in the events ledger, so a schedd restart or a transient Postgres
// reconnect can replay the recent window; deterministic invocation IDs make
// this sweep idempotent with the notification fast path.
func (l *Loop) runEventFanoutSweep(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	now := time.Now().UTC()
	if l.now != nil {
		now = l.now().UTC()
	}
	rows, err := l.engine.store.ListAllEventsPaged(ctx, "", "event.published", "", now.Add(-eventFanoutRecoveryWindow), eventFanoutRecoveryBatch)
	if err != nil {
		if l.log != nil {
			l.log.Warn("sched: event fanout recovery sweep failed", "err", err)
		}
		return
	}
	for _, row := range rows {
		if err := l.routePublishedEvent(ctx, string(row.Data)); err != nil && l.log != nil {
			l.log.Warn("sched: event fanout recovery failed", "event_id", row.ID, "err", err)
		}
	}
}
