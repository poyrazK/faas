package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PublishedEventWork is one durable fanout receipt. A claim token prevents a
// worker whose lease expired from acknowledging another worker's claim.
type PublishedEventWork struct {
	ID      int64
	Payload []byte
	// A nil database snapshot marks a receipt accepted before the snapshot
	// migration. SnapshotCaptured distinguishes it from an empty recipient set.
	RecipientSnapshot []PublishedEventRecipient
	SnapshotCaptured  bool
	ClaimToken        string
	Attempts          int
	AvailableAt       time.Time
	CreatedAt         time.Time
	LeaseUntil        time.Time
	Delivered         bool
	DeliveredAt       time.Time
}

// PublishedEventRecipient is an immutable source/type candidate captured when
// the event was accepted. The data filter is evaluated by the scheduler.
type PublishedEventRecipient struct {
	ID        string          `json:"id"`
	AccountID string          `json:"account_id"`
	AppID     string          `json:"app_id"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Filter    json.RawMessage `json:"filter"`
}

type PublishedEventWorkStore interface {
	ClaimDuePublishedEvent(context.Context, time.Time) (*PublishedEventWork, error)
	FinishPublishedEvent(context.Context, int64, string, error) error
}

const PublishedEventLease = 5 * time.Minute
const PublishedEventIdentityRetention = 30 * 24 * time.Hour

type PublishedEventRetentionStore interface {
	PruneDeliveredPublishedEvents(context.Context, time.Time, int) (int64, error)
}

func (s *PgStore) ClaimDuePublishedEvent(ctx context.Context, now time.Time) (*PublishedEventWork, error) {
	var work PublishedEventWork
	var payload []byte
	var snapshot []byte
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM event_fanout_outbox
		WHERE (state = 'pending' AND available_at <= $1)
		   OR (state = 'processing' AND lease_until <= $1)
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE event_fanout_outbox AS o
	SET state = 'processing', claim_token = gen_random_uuid(),
	    lease_until = $1 + interval '5 minutes', attempts = attempts + 1
	FROM candidate WHERE o.id = candidate.id
	RETURNING o.id, o.payload, o.recipient_snapshot, o.claim_token::text, o.attempts, o.lease_until, o.created_at`, now.UTC()).Scan(
		&work.ID, &payload, &snapshot, &work.ClaimToken, &work.Attempts, &work.LeaseUntil, &work.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("claim published event: %w", err)
	}
	work.Payload = payload
	if snapshot != nil {
		work.SnapshotCaptured = true
		if err := json.Unmarshal(snapshot, &work.RecipientSnapshot); err != nil {
			return nil, fmt.Errorf("decode published event recipient snapshot: %w", err)
		}
	}
	return &work, nil
}

func (s *PgStore) FinishPublishedEvent(ctx context.Context, id int64, token string, routeErr error) error {
	if routeErr == nil {
		result, err := s.pool.Exec(ctx, `UPDATE event_fanout_outbox SET state = 'delivered',
			delivered_at = now(), claim_token = NULL, lease_until = NULL, last_error = NULL
			WHERE id = $1 AND claim_token = $2::uuid AND state = 'processing'`, id, token)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrConflict
		}
		return nil
	}
	result, err := s.pool.Exec(ctx, `UPDATE event_fanout_outbox SET state = 'pending',
		available_at = now() + make_interval(secs => least(300, 5 * (1 << least(attempts - 1, 6)))),
		claim_token = NULL, lease_until = NULL, last_error = left($3, 1024)
		WHERE id = $1 AND claim_token = $2::uuid AND state = 'processing'`, id, token, routeErr.Error())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) PruneDeliveredPublishedEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM event_fanout_outbox WHERE id IN (
		SELECT id FROM event_fanout_outbox WHERE state = 'delivered' AND delivered_at < $1
		ORDER BY delivered_at, id LIMIT $2
	)`, before.UTC(), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

type publishedEventIdentity struct {
	Source        string          `json:"source"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SchemaVersion string          `json:"schemaversion"`
	Data          json.RawMessage `json:"data"`
}

func (m *MemStore) enqueuePublishedEventLocked(subject *uuid.UUID, payload []byte, now time.Time) error {
	if subject == nil {
		return fmt.Errorf("event.published requires an account subject")
	}
	var event publishedEventIdentity
	if err := json.Unmarshal(payload, &event); err != nil {
		return err
	}
	if event.Source == "" || event.ID == "" || event.Type == "" || len(event.Data) == 0 {
		return fmt.Errorf("event.published requires source, id, type and data")
	}
	key := subject.String() + "\x00" + event.Source + "\x00" + event.ID
	if previous := m.eventFanout[key]; previous != nil {
		var prior publishedEventIdentity
		_ = json.Unmarshal(previous.Payload, &prior)
		if prior.Type != event.Type || prior.SchemaVersion != event.SchemaVersion || !jsonEqual(prior.Data, event.Data) {
			return ErrConflict
		}
		return nil
	}
	if m.eventFanout == nil {
		m.eventFanout = make(map[string]*PublishedEventWork)
	}
	candidates := make([]EventSubscription, 0)
	for _, subscription := range m.eventSubscriptions {
		app, exists := m.eventSubscriptionAppLocked(subscription.AppID)
		if !sameMemUUID(subscription.AccountID, subject.String()) || !subscription.Enabled || !exists || app.Status == AppDeleted {
			continue
		}
		if eventSubscriptionPatternMatches(subscription.Source, event.Source) && eventSubscriptionPatternMatches(subscription.Type, event.Type) {
			candidates = append(candidates, subscription)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	recipients := make([]PublishedEventRecipient, 0, len(candidates))
	for _, row := range candidates {
		recipients = append(recipients, PublishedEventRecipient{ID: row.ID, AccountID: row.AccountID,
			AppID: row.AppID, Source: row.Source, Type: row.Type, Filter: bytes.Clone(row.Filter)})
	}
	m.eventFanoutNextID++
	m.eventFanout[key] = &PublishedEventWork{ID: m.eventFanoutNextID, Payload: bytes.Clone(payload), RecipientSnapshot: recipients,
		SnapshotCaptured: true, AvailableAt: now, CreatedAt: time.Now().UTC()}
	return nil
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	leftDecoder := json.NewDecoder(bytes.NewReader(a))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(b))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&left) != nil || rightDecoder.Decode(&right) != nil {
		return false
	}
	canonicalLeft, _ := json.Marshal(left)
	canonicalRight, _ := json.Marshal(right)
	return bytes.Equal(canonicalLeft, canonicalRight)
}

func (m *MemStore) ClaimDuePublishedEvent(_ context.Context, now time.Time) (*PublishedEventWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var chosen *PublishedEventWork
	for _, work := range m.eventFanout {
		if work.Delivered || (work.ClaimToken != "" && work.LeaseUntil.After(now)) || work.AvailableAt.After(now) {
			continue
		}
		if chosen == nil || work.ID < chosen.ID {
			chosen = work
		}
	}
	if chosen == nil {
		return nil, ErrNotFound
	}
	chosen.ClaimToken = uuid.NewString()
	chosen.LeaseUntil = now.Add(PublishedEventLease)
	chosen.Attempts++
	copy := *chosen
	copy.Payload = bytes.Clone(chosen.Payload)
	copy.RecipientSnapshot = make([]PublishedEventRecipient, len(chosen.RecipientSnapshot))
	for i, recipient := range chosen.RecipientSnapshot {
		copy.RecipientSnapshot[i] = recipient
		copy.RecipientSnapshot[i].Filter = bytes.Clone(recipient.Filter)
	}
	return &copy, nil
}

func (m *MemStore) FinishPublishedEvent(_ context.Context, id int64, token string, routeErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, work := range m.eventFanout {
		if work.ID != id {
			continue
		}
		if work.ClaimToken != token || token == "" {
			return ErrConflict
		}
		work.ClaimToken = ""
		work.LeaseUntil = time.Time{}
		if routeErr == nil {
			work.Delivered = true
			work.DeliveredAt = time.Now().UTC()
		} else {
			work.AvailableAt = time.Now().Add(5 * time.Second)
		}
		return nil
	}
	return ErrNotFound
}

func (m *MemStore) PruneDeliveredPublishedEvents(_ context.Context, before time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pruned int64
	for key, work := range m.eventFanout {
		if int(pruned) >= limit {
			break
		}
		if work.Delivered && work.DeliveredAt.Before(before) {
			delete(m.eventFanout, key)
			pruned++
		}
	}
	return pruned, nil
}
