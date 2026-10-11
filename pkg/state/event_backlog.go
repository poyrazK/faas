package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventBacklogStore interface {
	EventBacklog(context.Context, string, EventBacklogQuery) (EventBacklog, error)
}

type EventBacklogPosition struct {
	AcceptedAt     time.Time `json:"accepted_at"`
	OutboxID       int64     `json:"outbox_id"`
	SubscriptionID string    `json:"subscription_id"`
}
type EventBacklogConsumerPosition struct {
	AppID          string `json:"app_id"`
	SubscriptionID string `json:"subscription_id"`
}
type EventBacklogQuery struct {
	Filters              api.EventBacklogFilters
	AppID                string
	WindowAt             time.Time
	After                EventBacklogPosition
	ConsumersAfter       EventBacklogConsumerPosition
	Limit, ConsumerLimit int
}
type EventBacklogEntry struct {
	api.EventBacklogRecipient
	OutboxID int64
}
type EventBacklog struct {
	ObservedAt, WindowAt time.Time
	Recipients           []EventBacklogEntry
	Consumers            []api.EventBacklogConsumer
	UnattributedReceipts int64
	Next                 EventBacklogPosition
	NextConsumer         EventBacklogConsumerPosition
}

func validateEventBacklog(accountID string, q *EventBacklogQuery) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return ErrInvalidArgument
	}
	for _, id := range []string{q.AppID, q.ConsumersAfter.AppID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return ErrInvalidArgument
			}
		}
	}
	if err := q.Filters.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if q.After.OutboxID < 0 || q.After.OutboxID > 0 && (q.After.AcceptedAt.IsZero() || q.After.SubscriptionID == "") {
		return ErrInvalidArgument
	}
	if q.WindowAt.IsZero() {
		q.WindowAt = time.Now().UTC()
	}
	if q.Limit <= 0 {
		q.Limit = api.EventBacklogPageDefault
	}
	if q.ConsumerLimit <= 0 {
		q.ConsumerLimit = api.EventBacklogPageDefault
	}
	q.Limit = min(q.Limit, api.EventBacklogPageMax)
	q.ConsumerLimit = min(q.ConsumerLimit, api.EventBacklogPageMax)
	return nil
}

func newEventBacklog(q EventBacklogQuery) EventBacklog {
	return EventBacklog{ObservedAt: time.Now().UTC(), WindowAt: q.WindowAt, Recipients: []EventBacklogEntry{}, Consumers: []api.EventBacklogConsumer{}}
}
func backlogUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return mustPgUUID(id)
}
func backlogCutoff(q EventBacklogQuery) time.Time {
	return q.WindowAt.Add(-time.Duration(q.Filters.MinAgeSeconds) * time.Second)
}

func (s *PgStore) EventBacklog(ctx context.Context, accountID string, query EventBacklogQuery) (EventBacklog, error) {
	if err := validateEventBacklog(accountID, &query); err != nil {
		return EventBacklog{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return EventBacklog{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	result := newEventBacklog(query)
	id, appID, cutoff := mustPgUUID(accountID), backlogUUID(query.AppID), nullableTimestamptz(backlogCutoff(query))
	rows, err := q.EventBacklogRecipients(ctx, tx, sqlc.EventBacklogRecipientsParams{AccountID: id, AppID: appID, Cutoff: cutoff,
		SubscriptionID: query.Filters.SubscriptionID, RoutingState: query.Filters.State, CapacityScope: query.Filters.CapacityScope,
		AfterAcceptedAt: nullableTimestamptz(query.After.AcceptedAt), AfterOutboxID: query.After.OutboxID, AfterSubscriptionID: query.After.SubscriptionID, PageLimit: int32(query.Limit + 1)})
	if err != nil {
		return EventBacklog{}, fmt.Errorf("read backlog recipients: %w", err)
	}
	for _, row := range rows {
		entry := EventBacklogEntry{OutboxID: row.OutboxID, EventBacklogRecipient: api.EventBacklogRecipient{
			EventSource: row.Source, EventID: row.EventID, EventType: row.EventType, AcceptedAt: timeFromPgtype(row.AcceptedAt),
			AppID: uuidFromPgtype(row.AppID).String(), AppSlug: row.AppSlug, TargetAvailable: row.TargetAvailable, SubscriptionID: row.SubscriptionID,
			RoutingMode: row.RoutingMode, State: row.RoutingState, CapacityScope: row.CapacityScope, Attempts: int(row.Attempts), CapacityDeferrals: int(row.CapacityDeferrals),
			NextAttemptAt: timestamptzToTimePtr(row.NextAttemptAt), LeaseUntil: timestamptzToTimePtr(row.LeaseUntil)}}
		backlogObservation(&entry.EventBacklogRecipient, result.ObservedAt)
		result.Recipients = append(result.Recipients, entry)
	}
	consumers, err := q.EventBacklogConsumers(ctx, tx, sqlc.EventBacklogConsumersParams{AccountID: id, AppID: appID, Cutoff: cutoff,
		SubscriptionID: query.Filters.SubscriptionID, RoutingState: query.Filters.State, CapacityScope: query.Filters.CapacityScope,
		AfterAppID: backlogUUID(query.ConsumersAfter.AppID), AfterSubscriptionID: query.ConsumersAfter.SubscriptionID, PageLimit: int32(query.ConsumerLimit + 1)})
	if err != nil {
		return EventBacklog{}, fmt.Errorf("read backlog consumers: %w", err)
	}
	for _, row := range consumers {
		oldest := timeFromPgtype(row.OldestAcceptedAt)
		result.Consumers = append(result.Consumers, api.EventBacklogConsumer{AppID: uuidFromPgtype(row.AppID).String(), AppSlug: row.AppSlug, TargetAvailable: row.TargetAvailable,
			SubscriptionID: row.SubscriptionID, WaitingRecipients: row.WaitingRecipients, PendingRecipients: row.PendingRecipients, ProcessingRecipients: row.ProcessingRecipients,
			CapacityWaitingRecipients: row.CapacityWaitingRecipients, OldestAcceptedAt: oldest, OldestAgeSeconds: max(0, result.ObservedAt.Sub(oldest).Seconds())})
	}
	result.UnattributedReceipts, err = q.EventBacklogUnattributed(ctx, tx, sqlc.EventBacklogUnattributedParams{AccountID: id, Cutoff: cutoff})
	if err != nil {
		return EventBacklog{}, err
	}
	backlogPages(&result, query)
	if err = tx.Commit(ctx); err != nil {
		return EventBacklog{}, err
	}
	return result, nil
}

func backlogObservation(r *api.EventBacklogRecipient, now time.Time) {
	r.PendingAgeSeconds = max(0, now.Sub(r.AcceptedAt).Seconds())
	switch {
	case r.State == "pending" && r.CapacityScope != "":
		r.WaitingReason = "capacity_" + r.CapacityScope
	case r.State == "processing":
		r.WaitingReason = "routing_in_progress"
	case r.RoutingMode == "event" && r.LeaseUntil != nil && r.LeaseUntil.After(now):
		r.WaitingReason = "receipt_processing"
	case r.NextAttemptAt != nil && r.NextAttemptAt.After(now):
		r.WaitingReason = "retry_backoff"
	default:
		r.WaitingReason = "ready"
	}
}

func backlogPosition(e EventBacklogEntry) EventBacklogPosition {
	return EventBacklogPosition{AcceptedAt: e.AcceptedAt, OutboxID: e.OutboxID, SubscriptionID: e.SubscriptionID}
}
func backlogPages(result *EventBacklog, q EventBacklogQuery) {
	if len(result.Recipients) > q.Limit {
		result.Next = backlogPosition(result.Recipients[q.Limit-1])
		result.Recipients = result.Recipients[:q.Limit]
	}
	if len(result.Consumers) > q.ConsumerLimit {
		last := result.Consumers[q.ConsumerLimit-1]
		result.NextConsumer = EventBacklogConsumerPosition{AppID: last.AppID, SubscriptionID: last.SubscriptionID}
		result.Consumers = result.Consumers[:q.ConsumerLimit]
	}
}
func backlogAfter(a, b EventBacklogPosition) bool {
	if b.OutboxID == 0 {
		return true
	}
	if !a.AcceptedAt.Equal(b.AcceptedAt) {
		return a.AcceptedAt.After(b.AcceptedAt)
	}
	if a.OutboxID != b.OutboxID {
		return a.OutboxID > b.OutboxID
	}
	return a.SubscriptionID > b.SubscriptionID
}
func backlogConsumerAfter(app, sub string, c EventBacklogConsumerPosition) bool {
	return c.AppID == "" || app > c.AppID || app == c.AppID && sub > c.SubscriptionID
}

func (m *MemStore) EventBacklog(ctx context.Context, accountID string, query EventBacklogQuery) (EventBacklog, error) {
	if err := validateEventBacklog(accountID, &query); err != nil {
		return EventBacklog{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := newEventBacklog(query)
	consumers := map[EventBacklogConsumerPosition]*api.EventBacklogConsumer{}
	prefix := canonicalMemUUID(accountID) + "\x00"
	cutoff := backlogCutoff(query)
	for key, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return EventBacklog{}, err
		}
		if !strings.HasPrefix(key, prefix) || work.Delivered || work.CreatedAt.After(cutoff) {
			continue
		}
		if !work.SnapshotCaptured {
			result.UnattributedReceipts++
			continue
		}
		var identity publishedEventIdentity
		if err := json.Unmarshal(work.Payload, &identity); err != nil {
			return EventBacklog{}, err
		}
		for _, recipient := range work.RecipientSnapshot {
			if recipient.AppID == "" || len(recipient.Workflow) != 0 {
				continue
			}
			entry := m.backlogRecipientLocked(accountID, work, recipient, identity)
			if !backlogMatches(entry, query) {
				continue
			}
			backlogObservation(&entry.EventBacklogRecipient, result.ObservedAt)
			backlogConsumerObserve(consumers, entry, result.ObservedAt)
			if backlogAfter(backlogPosition(entry), query.After) {
				result.Recipients = append(result.Recipients, entry)
			}
		}
	}
	for key, c := range consumers {
		if backlogConsumerAfter(key.AppID, key.SubscriptionID, query.ConsumersAfter) {
			result.Consumers = append(result.Consumers, *c)
		}
	}
	sort.Slice(result.Recipients, func(i, j int) bool {
		return backlogAfter(backlogPosition(result.Recipients[j]), backlogPosition(result.Recipients[i]))
	})
	sort.Slice(result.Consumers, func(i, j int) bool {
		a, b := result.Consumers[i], result.Consumers[j]
		return a.AppID < b.AppID || a.AppID == b.AppID && a.SubscriptionID < b.SubscriptionID
	})
	backlogPages(&result, query)
	return result, nil
}

func (m *MemStore) backlogRecipientLocked(accountID string, w *PublishedEventWork, r PublishedEventRecipient, identity publishedEventIdentity) EventBacklogEntry {
	p := w.RecipientProgress[r.ID]
	e := EventBacklogEntry{OutboxID: w.ID, EventBacklogRecipient: api.EventBacklogRecipient{EventSource: identity.Source, EventID: identity.ID, EventType: identity.Type,
		AcceptedAt: w.CreatedAt, AppID: canonicalMemUUID(r.AppID), SubscriptionID: r.ID, RoutingMode: "event", State: p.State, Attempts: p.Attempts, CapacityDeferrals: p.CapacityDeferrals,
		CapacityScope: p.CapacityScope, NextAttemptAt: cloneEventReceiptTime(p.NextAttemptAt)}}
	if e.State == "" {
		e.State = "pending"
	}
	if w.RecipientClaims {
		e.RoutingMode = "recipient"
		if own := w.routingRecipients[r.ID]; own != nil {
			e.State, e.Attempts = own.State, own.TotalAttempts
			e.CapacityDeferrals = max(e.CapacityDeferrals, own.CapacityDeferrals)
			if e.State == "pending" {
				e.NextAttemptAt = eventBacklogTime(own.AvailableAt)
			}
			e.LeaseUntil = eventBacklogTime(own.LeaseUntil)
		}
	} else {
		e.LeaseUntil = eventBacklogTime(w.LeaseUntil)
		if e.State == "pending" && e.NextAttemptAt == nil && w.ClaimToken == "" {
			e.NextAttemptAt = eventBacklogTime(w.AvailableAt)
		}
	}
	if e.State != "pending" {
		e.CapacityScope = ""
		e.NextAttemptAt = nil
	}
	app, exists := m.eventSubscriptionAppLocked(r.AppID)
	if exists && sameMemUUID(app.AccountID, accountID) {
		e.AppSlug = app.Slug
		e.TargetAvailable = app.Status != AppDeleted
	}
	return e
}
func eventBacklogTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
func backlogMatches(e EventBacklogEntry, q EventBacklogQuery) bool {
	return (e.State == "pending" || e.State == "processing") && (q.AppID == "" || sameMemUUID(e.AppID, q.AppID)) &&
		(q.Filters.SubscriptionID == "" || e.SubscriptionID == q.Filters.SubscriptionID) &&
		(q.Filters.State == "" || e.State == q.Filters.State) && (q.Filters.CapacityScope == "" || e.CapacityScope == q.Filters.CapacityScope)
}
func backlogConsumerObserve(cs map[EventBacklogConsumerPosition]*api.EventBacklogConsumer, e EventBacklogEntry, now time.Time) {
	key := EventBacklogConsumerPosition{AppID: e.AppID, SubscriptionID: e.SubscriptionID}
	c := cs[key]
	if c == nil {
		c = &api.EventBacklogConsumer{AppID: e.AppID, AppSlug: e.AppSlug, TargetAvailable: e.TargetAvailable, SubscriptionID: e.SubscriptionID, OldestAcceptedAt: e.AcceptedAt}
		cs[key] = c
	}
	c.WaitingRecipients++
	if e.State == "pending" {
		c.PendingRecipients++
	} else {
		c.ProcessingRecipients++
	}
	if e.CapacityScope != "" {
		c.CapacityWaitingRecipients++
	}
	if e.AcceptedAt.Before(c.OldestAcceptedAt) {
		c.OldestAcceptedAt = e.AcceptedAt
	}
	c.OldestAgeSeconds = max(0, now.Sub(c.OldestAcceptedAt).Seconds())
}
