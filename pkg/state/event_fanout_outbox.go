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
	RecipientProgress map[string]PublishedEventRecipientProgress
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

// PublishedEventRecipientProgress records scheduler-side fanout progress for
// one candidate. Invocation execution retries are tracked on the invocation.
type PublishedEventRecipientProgress struct {
	State     string    `json:"state"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EventFanoutFailure is a terminal routing failure recorded before an
// invocation exists. OutboxID is only used to build a stable page cursor.
type EventFanoutFailure struct {
	OutboxID       int64
	EventID        string
	EventSource    string
	EventType      string
	SubscriptionID string
	Attempts       int
	LastError      string
	CreatedAt      time.Time
	FailedAt       time.Time
}

// EventFanoutFailureCursor is the keyset cursor for failed recipients. One
// outbox event can contribute multiple failures, so the subscription ID
// disambiguates rows with the same outbox ID.
type EventFanoutFailureCursor struct {
	CreatedAt      time.Time
	OutboxID       int64
	SubscriptionID string
}

const (
	PublishedEventRecipientPending  = "pending"
	PublishedEventRecipientFiltered = "filtered"
	PublishedEventRecipientEnqueued = "enqueued"
	PublishedEventRecipientFailed   = "failed"
)

type PublishedEventWorkStore interface {
	ClaimDuePublishedEvent(context.Context, time.Time) (*PublishedEventWork, error)
	FinishPublishedEvent(context.Context, int64, string, error) error
}

// PublishedEventRecipientProgressStore persists each recipient outcome while
// an event receipt is claimed, so one failed candidate cannot replay successful
// candidates after a worker restart.
type PublishedEventRecipientProgressStore interface {
	RecordPublishedEventRecipientProgress(context.Context, int64, string, string, PublishedEventRecipientProgress) error
}

// EventFanoutFailureStore exposes bounded, app-scoped inspection of terminal
// recipient failures that did not reach the invocation lifecycle.
type EventFanoutFailureStore interface {
	ListEventFanoutFailuresForApp(context.Context, string, int, EventFanoutFailureCursor, string) ([]EventFanoutFailure, error)
}

// EventFanoutReplayStore retries one terminal recipient using the exact
// acceptance-time candidate captured on the published event receipt.
type EventFanoutReplayStore interface {
	ReplayFailedPublishedEventRecipientForApp(context.Context, string, string, string, string, string) error
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
	var progress []byte
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM event_fanout_outbox
		WHERE (state = 'pending' AND available_at <= $1)
		   OR (state = 'processing' AND lease_until <= $1)
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE event_fanout_outbox AS o
	SET state = 'processing', claim_token = gen_random_uuid(),
	    lease_until = $1 + interval '5 minutes', attempts = attempts + 1
	FROM candidate WHERE o.id = candidate.id
	RETURNING o.id, o.payload, o.recipient_snapshot, o.recipient_progress,
	          o.claim_token::text, o.attempts, o.lease_until, o.created_at`, now.UTC()).Scan(
		&work.ID, &payload, &snapshot, &progress, &work.ClaimToken, &work.Attempts, &work.LeaseUntil, &work.CreatedAt)
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
	if progress != nil {
		if err := json.Unmarshal(progress, &work.RecipientProgress); err != nil {
			return nil, fmt.Errorf("decode published event recipient progress: %w", err)
		}
	}
	return &work, nil
}

func (s *PgStore) RecordPublishedEventRecipientProgress(ctx context.Context, id int64, token, recipientID string, progress PublishedEventRecipientProgress) error {
	if err := validatePublishedEventRecipientProgress(progress); err != nil {
		return err
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `UPDATE event_fanout_outbox AS o
		SET recipient_progress = jsonb_set(o.recipient_progress, ARRAY[$3::text], $4::jsonb, true),
		    last_error = CASE WHEN $4::jsonb->>'state' = 'failed'
		        THEN left('subscription ' || $3 || ': ' || coalesce($4::jsonb->>'last_error', 'recipient failed'), 1024)
		        ELSE o.last_error END
		WHERE o.id = $1 AND o.claim_token = $2::uuid AND o.state = 'processing'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) AS recipients(recipient)
		              WHERE recipient->>'id' = $3)`, id, token, recipientID, encoded)
	if err != nil {
		return fmt.Errorf("record published event recipient progress: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func validatePublishedEventRecipientProgress(progress PublishedEventRecipientProgress) error {
	switch progress.State {
	case PublishedEventRecipientPending, PublishedEventRecipientFiltered,
		PublishedEventRecipientEnqueued, PublishedEventRecipientFailed:
	default:
		return fmt.Errorf("state: invalid published event recipient state %q", progress.State)
	}
	if progress.Attempts < 0 {
		return fmt.Errorf("state: published event recipient attempts cannot be negative")
	}
	return nil
}

func (s *PgStore) FinishPublishedEvent(ctx context.Context, id int64, token string, routeErr error) error {
	if routeErr == nil {
		result, err := s.pool.Exec(ctx, `UPDATE event_fanout_outbox AS o SET state = 'delivered',
			delivered_at = now(), claim_token = NULL, lease_until = NULL,
			last_error = (SELECT left('subscription ' || progress.key || ': ' ||
				coalesce(progress.outcome->>'last_error', 'recipient failed'), 1024)
				FROM jsonb_each(o.recipient_progress) AS progress(key, outcome)
				WHERE progress.outcome->>'state' = 'failed'
				ORDER BY progress.key LIMIT 1)
			WHERE o.id = $1 AND o.claim_token = $2::uuid AND o.state = 'processing'`, id, token)
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

// ListEventFanoutFailuresForApp returns failed recipient outcomes newest
// first. The app/account predicates are tied to the immutable acceptance-time
// recipient snapshot, so removed subscriptions remain inspectable without
// widening the authenticated app scope.
func (s *PgStore) ListEventFanoutFailuresForApp(ctx context.Context, appID string, limit int, before EventFanoutFailureCursor, eventID string) ([]EventFanoutFailure, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.event_id, o.source, o.event_type,
		p.key, COALESCE(NULLIF(p.outcome->>'attempts', '')::int, 0),
		COALESCE(p.outcome->>'last_error', ''), o.created_at,
		COALESCE((p.outcome->>'updated_at')::timestamptz, o.created_at)
	FROM event_fanout_outbox o
	JOIN apps a ON a.id = $1 AND a.account_id = o.account_id
	CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
	CROSS JOIN LATERAL jsonb_each(COALESCE(o.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
	WHERE o.last_error IS NOT NULL
	  AND r.recipient->>'app_id' = a.id::text
	  AND r.recipient->>'id' = p.key
	  AND p.outcome->>'state' = 'failed'
	  AND ($2 = '' OR o.event_id = $2)
	  AND ($3::bigint = 0 OR (o.created_at, o.id, p.key) < ($4::timestamptz, $3, $5))
	ORDER BY o.created_at DESC, o.id DESC, p.key DESC
	LIMIT $6`, appID, eventID, before.OutboxID, before.CreatedAt, before.SubscriptionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]EventFanoutFailure, 0)
	for rows.Next() {
		var failure EventFanoutFailure
		if err := rows.Scan(&failure.OutboxID, &failure.EventID, &failure.EventSource, &failure.EventType,
			&failure.SubscriptionID, &failure.Attempts, &failure.LastError, &failure.CreatedAt, &failure.FailedAt); err != nil {
			return nil, err
		}
		out = append(out, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplayFailedPublishedEventRecipientForApp resets only the selected terminal
// candidate. It requires the receipt to be delivered so an active fanout claim
// cannot race the operator action. The original recipient snapshot is kept.
func (s *PgStore) ReplayFailedPublishedEventRecipientForApp(ctx context.Context, accountID, appID, eventSource, eventID, subscriptionID string) error {
	result, err := s.pool.Exec(ctx, `UPDATE event_fanout_outbox AS o
		SET state = 'pending', available_at = now(), delivered_at = NULL,
		    claim_token = NULL, lease_until = NULL,
		    recipient_progress = jsonb_set(o.recipient_progress, ARRAY[$5::text],
		        jsonb_build_object('state', 'pending',
		            'attempts', COALESCE(NULLIF((o.recipient_progress -> $5)->>'attempts', '')::int, 0),
		            'updated_at', now()), false),
		    last_error = (SELECT left('subscription ' || progress.key || ': ' ||
		        coalesce(progress.outcome->>'last_error', 'recipient failed'), 1024)
		        FROM jsonb_each(o.recipient_progress) AS progress(key, outcome)
		        WHERE progress.key <> $5 AND progress.outcome->>'state' = 'failed'
		        ORDER BY progress.key LIMIT 1)
		WHERE o.account_id = $1::uuid AND o.source = $3 AND o.event_id = $4
		  AND o.state = 'delivered'
		  AND (o.recipient_progress -> $5)->>'state' = 'failed'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		              WHERE r.recipient->>'app_id' = $2::text AND r.recipient->>'id' = $5)`,
		accountID, appID, eventSource, eventID, subscriptionID)
	if err != nil {
		return fmt.Errorf("replay published event recipient: %w", err)
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	var failed bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM event_fanout_outbox AS o
		WHERE o.account_id = $1::uuid AND o.source = $3 AND o.event_id = $4
		  AND (o.recipient_progress -> $5)->>'state' = 'failed'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		              WHERE r.recipient->>'app_id' = $2::text AND r.recipient->>'id' = $5)
	)`, accountID, appID, eventSource, eventID, subscriptionID).Scan(&failed)
	if err != nil {
		return fmt.Errorf("inspect published event recipient replay: %w", err)
	}
	if failed {
		return ErrConflict
	}
	return ErrNotFound
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
		SnapshotCaptured: true, RecipientProgress: make(map[string]PublishedEventRecipientProgress),
		AvailableAt: now, CreatedAt: time.Now().UTC()}
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
	copy.RecipientProgress = make(map[string]PublishedEventRecipientProgress, len(chosen.RecipientProgress))
	for id, progress := range chosen.RecipientProgress {
		copy.RecipientProgress[id] = progress
	}
	return &copy, nil
}

func (m *MemStore) RecordPublishedEventRecipientProgress(_ context.Context, id int64, token, recipientID string, progress PublishedEventRecipientProgress) error {
	if err := validatePublishedEventRecipientProgress(progress); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, work := range m.eventFanout {
		if work.ID != id {
			continue
		}
		if work.ClaimToken != token || token == "" || work.Delivered {
			return ErrConflict
		}
		found := false
		for _, recipient := range work.RecipientSnapshot {
			if recipient.ID == recipientID {
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
		if work.RecipientProgress == nil {
			work.RecipientProgress = make(map[string]PublishedEventRecipientProgress)
		}
		work.RecipientProgress[recipientID] = progress
		return nil
	}
	return ErrNotFound
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

// ListEventFanoutFailuresForApp mirrors the PostgreSQL projection for tests
// and in-memory API use.
func (m *MemStore) ListEventFanoutFailuresForApp(_ context.Context, appID string, limit int, before EventFanoutFailureCursor, eventID string) ([]EventFanoutFailure, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]EventFanoutFailure, 0)
	for _, work := range m.eventFanout {
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil || (eventID != "" && event.ID != eventID) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if recipient.AppID != appID {
				continue
			}
			progress, ok := work.RecipientProgress[recipient.ID]
			if !ok || progress.State != PublishedEventRecipientFailed {
				continue
			}
			if before.OutboxID > 0 && (work.CreatedAt.After(before.CreatedAt) ||
				(work.CreatedAt.Equal(before.CreatedAt) && (work.ID > before.OutboxID ||
					(work.ID == before.OutboxID && recipient.ID >= before.SubscriptionID)))) {
				continue
			}
			out = append(out, EventFanoutFailure{
				OutboxID: work.ID, EventID: event.ID, EventSource: event.Source, EventType: event.Type,
				SubscriptionID: recipient.ID, Attempts: progress.Attempts, LastError: progress.LastError,
				CreatedAt: work.CreatedAt, FailedAt: progress.UpdatedAt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		if out[i].OutboxID == out[j].OutboxID {
			return out[i].SubscriptionID > out[j].SubscriptionID
		}
		return out[i].OutboxID > out[j].OutboxID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ReplayFailedPublishedEventRecipientForApp mirrors the PostgreSQL state
// transition and preserves all other recipient outcomes and the snapshot.
func (m *MemStore) ReplayFailedPublishedEventRecipientForApp(_ context.Context, accountID, appID, eventSource, eventID, subscriptionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, work := range m.eventFanout {
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil || event.ID != eventID || event.Source != eventSource {
			continue
		}
		var recipientFound bool
		for _, recipient := range work.RecipientSnapshot {
			if recipient.ID == subscriptionID && recipient.AppID == appID && recipient.AccountID == accountID {
				recipientFound = true
				break
			}
		}
		if !recipientFound {
			continue
		}
		progress, failed := work.RecipientProgress[subscriptionID]
		if !failed || progress.State != PublishedEventRecipientFailed {
			return ErrNotFound
		}
		if !work.Delivered {
			return ErrConflict
		}
		progress.State = PublishedEventRecipientPending
		progress.LastError = ""
		progress.UpdatedAt = time.Now().UTC()
		work.RecipientProgress[subscriptionID] = progress
		work.Delivered = false
		work.DeliveredAt = time.Time{}
		work.AvailableAt = time.Now().UTC()
		work.ClaimToken = ""
		work.LeaseUntil = time.Time{}
		return nil
	}
	return ErrNotFound
}
