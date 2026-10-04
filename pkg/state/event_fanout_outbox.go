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
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
	ID                   string                             `json:"id"`
	AccountID            string                             `json:"account_id"`
	AppID                string                             `json:"app_id"`
	Source               string                             `json:"source"`
	Type                 string                             `json:"type"`
	Filter               json.RawMessage                    `json:"filter"`
	WorkSnapshotCaptured bool                               `json:"work_snapshot_captured,omitempty"`
	Work                 *PublishedEventWorkBindingSnapshot `json:"work,omitempty"`
	ObjectNotification   *ObjectNotificationSnapshot        `json:"object_notification,omitempty"`
}

// PublishedEventWorkBindingSnapshot keeps event routing stable when a binding
// changes after the event was accepted. Old snapshots have no captured flag
// and continue to use the live binding for backward compatibility.
type PublishedEventWorkBindingSnapshot struct {
	PolicyName       string `json:"policy_name"`
	KeySelector      string `json:"key_selector"`
	FairnessSelector string `json:"fairness_selector,omitempty"`
	Action           string `json:"action"`
	// Nil on receipts accepted before policy snapshots were introduced.
	Policy *PublishedEventWorkPolicySnapshot `json:"policy,omitempty"`
}

// PublishedEventWorkPolicySnapshot preserves the admission settings of an
// accepted event even if the named policy changes or is removed before fanout.
type PublishedEventWorkPolicySnapshot struct {
	Revision                 int64                     `json:"revision"`
	MaxRunningPerKey         int                       `json:"max_running_per_key"`
	MaxRunningPerFairnessKey int                       `json:"max_running_per_fairness_key"`
	PendingUpdates           workpolicy.PendingUpdates `json:"pending_updates"`
	DebounceMS               int64                     `json:"debounce_ms"`
	ExpiresAfterMS           int64                     `json:"expires_after_ms"`
}

func snapshotPublishedEventWorkPolicy(record AppWorkPolicy) *PublishedEventWorkPolicySnapshot {
	return &PublishedEventWorkPolicySnapshot{
		Revision: record.Revision, MaxRunningPerKey: record.Policy.MaxRunningPerKey,
		MaxRunningPerFairnessKey: record.Policy.MaxRunningPerFairnessKey,
		PendingUpdates:           record.Policy.PendingUpdates,
		DebounceMS:               record.Policy.Debounce.Milliseconds(),
		ExpiresAfterMS:           record.Policy.ExpiresAfter.Milliseconds(),
	}
}

func (snapshot PublishedEventWorkPolicySnapshot) EffectivePolicy(name string) (workpolicy.Policy, error) {
	policy := workpolicy.Policy{
		Name: name, MaxRunningPerKey: snapshot.MaxRunningPerKey,
		MaxRunningPerFairnessKey: snapshot.MaxRunningPerFairnessKey,
		PendingUpdates:           snapshot.PendingUpdates,
		Debounce:                 time.Duration(snapshot.DebounceMS) * time.Millisecond,
		ExpiresAfter:             time.Duration(snapshot.ExpiresAfterMS) * time.Millisecond,
	}
	if snapshot.Revision < 1 {
		return workpolicy.Policy{}, fmt.Errorf("event work policy snapshot has invalid revision")
	}
	if err := policy.Validate(); err != nil {
		return workpolicy.Policy{}, err
	}
	return policy, nil
}

// PublishedEventRecipientProgress records scheduler-side fanout progress for
// one candidate. Invocation execution retries are tracked on the invocation.
type PublishedEventRecipientProgress struct {
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	FailureCode string    `json:"failure_code,omitempty"`
	Retryable   bool      `json:"retryable,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	FailureCode    string
	Retryable      bool
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

// EventFanoutAttempt records one routing outcome or an operator replay
// request for a captured event recipient. The mutable recipient progress is
// still the scheduler checkpoint; these rows retain the inspection history.
type EventFanoutAttempt struct {
	ID             int64
	OutboxID       int64
	AppID          string
	EventID        string
	EventSource    string
	EventType      string
	SubscriptionID string
	Action         string
	State          string
	Attempts       int
	FailureCode    string
	Retryable      bool
	LastError      string
	OccurredAt     time.Time
}

type EventFanoutAttemptCursor struct {
	ID int64
}

const (
	EventFanoutAttemptActionAttempt = "fanout_attempt"
	EventFanoutAttemptActionReplay  = "operator_replay"
)

// EventFanoutReplayBatch reports a bounded operator replay. HasMore means
// retryable terminal recipients remain and may become eligible after an
// in-progress event fanout settles.
type EventFanoutReplayBatch struct {
	Replayed int
	HasMore  bool
}

const (
	PublishedEventRecipientPending  = "pending"
	PublishedEventRecipientFiltered = "filtered"
	PublishedEventRecipientEnqueued = "enqueued"
	PublishedEventRecipientFailed   = "failed"
)

// Event fanout failure codes are a stable operator-facing classification.
const (
	EventFanoutFailureCodeUnknown                 = "unknown"
	EventFanoutFailureCodeInvalidSubscription     = "invalid_subscription"
	EventFanoutFailureCodeTargetUnavailable       = "target_unavailable"
	EventFanoutFailureCodeTargetLookupFailed      = "target_lookup_failed"
	EventFanoutFailureCodeInvocationEnqueueFailed = "invocation_enqueue_failed"
	EventFanoutFailureCodeInternal                = "internal_error"
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
	ListEventFanoutFailuresForApp(context.Context, string, int, EventFanoutFailureCursor, string, string) ([]EventFanoutFailure, error)
}

// EventFanoutAttemptHistoryStore exposes a bounded, event-scoped history of
// scheduler outcomes and explicit replay requests for one app's recipients.
type EventFanoutAttemptHistoryStore interface {
	ListEventFanoutAttemptsForApp(context.Context, string, int, EventFanoutAttemptCursor, string, string, string) ([]EventFanoutAttempt, error)
}

// EventFanoutReplayStore retries one terminal recipient using the exact
// acceptance-time candidate captured on the published event receipt.
type EventFanoutReplayStore interface {
	ReplayFailedPublishedEventRecipientForApp(context.Context, string, string, string, string, string) error
}

// EventFanoutReplayBatchStore requeues a bounded set of terminal recipients
// classified as retryable. Empty eventSource and eventID search the app's
// retained failure history; when either is non-empty, both scope the replay
// to one published event identity.
type EventFanoutReplayBatchStore interface {
	ReplayRetryablePublishedEventRecipientsForApp(context.Context, string, string, string, string, int) (EventFanoutReplayBatch, error)
}

const PublishedEventLease = 5 * time.Minute
const PublishedEventIdentityRetention = 30 * 24 * time.Hour
const EventFanoutReplayBatchMax = 100

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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin published event recipient progress: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	var appID string
	err = tx.QueryRow(ctx, `UPDATE event_fanout_outbox AS o
		SET recipient_progress = jsonb_set(o.recipient_progress, ARRAY[$3::text], $4::jsonb, true),
		    last_error = CASE WHEN $4::jsonb->>'state' = 'failed'
		        THEN left('subscription ' || $3 || ': ' || coalesce($4::jsonb->>'last_error', 'recipient failed'), 1024)
		        ELSE o.last_error END
		WHERE o.id = $1 AND o.claim_token = $2::uuid AND o.state = 'processing'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.recipient_snapshot) AS recipients(recipient)
		              WHERE recipient->>'id' = $3)
		RETURNING (SELECT recipient->>'app_id'
		          FROM jsonb_array_elements(o.recipient_snapshot) AS recipients(recipient)
		          WHERE recipient->>'id' = $3 LIMIT 1)`, id, token, recipientID, encoded).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("record published event recipient progress: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO event_fanout_attempt_history
		(outbox_id, app_id, subscription_id, action, state, attempts,
		 failure_code, retryable, last_error, occurred_at)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, appID, recipientID, EventFanoutAttemptActionAttempt, progress.State,
		progress.Attempts, progress.FailureCode, progress.Retryable, progress.LastError, progress.UpdatedAt); err != nil {
		return fmt.Errorf("append published event recipient attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit published event recipient progress: %w", err)
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
// first, optionally filtered by source and event ID. The app/account
// predicates are tied to the immutable acceptance-time recipient snapshot, so
// removed subscriptions remain inspectable without widening the authenticated
// app scope.
func (s *PgStore) ListEventFanoutFailuresForApp(ctx context.Context, appID string, limit int, before EventFanoutFailureCursor, eventSource, eventID string) ([]EventFanoutFailure, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.event_id, o.source, o.event_type,
		p.key, COALESCE(NULLIF(p.outcome->>'attempts', '')::int, 0),
		COALESCE(NULLIF(p.outcome->>'failure_code', ''), 'unknown'),
		COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false),
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
	  AND ($2 = '' OR o.source = $2)
	  AND ($3 = '' OR o.event_id = $3)
	  AND ($4::bigint = 0 OR (o.created_at, o.id, p.key) < ($5::timestamptz, $4, $6))
	ORDER BY o.created_at DESC, o.id DESC, p.key DESC
	LIMIT $7`, appID, eventSource, eventID, before.OutboxID, before.CreatedAt, before.SubscriptionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]EventFanoutFailure, 0)
	for rows.Next() {
		var failure EventFanoutFailure
		if err := rows.Scan(&failure.OutboxID, &failure.EventID, &failure.EventSource, &failure.EventType,
			&failure.SubscriptionID, &failure.Attempts, &failure.FailureCode, &failure.Retryable,
			&failure.LastError, &failure.CreatedAt, &failure.FailedAt); err != nil {
			return nil, err
		}
		out = append(out, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListEventFanoutAttemptsForApp returns recent routing outcomes and operator
// replay requests for one event identity. The captured recipient app and
// outbox account jointly enforce the authenticated app boundary.
func (s *PgStore) ListEventFanoutAttemptsForApp(ctx context.Context, appID string, limit int, before EventFanoutAttemptCursor, eventSource, eventID, subscriptionID string) ([]EventFanoutAttempt, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT h.id, h.outbox_id, h.app_id::text,
		o.event_id, o.source, o.event_type, h.subscription_id, h.action, h.state,
		h.attempts, h.failure_code, h.retryable, h.last_error, h.occurred_at
	FROM event_fanout_attempt_history h
	JOIN event_fanout_outbox o ON o.id = h.outbox_id
	JOIN apps a ON a.id = h.app_id AND a.account_id = o.account_id
	WHERE h.app_id = $1::uuid
	  AND ($2 = '' OR o.source = $2)
	  AND ($3 = '' OR o.event_id = $3)
	  AND ($4 = '' OR h.subscription_id = $4)
	  AND ($5::bigint = 0 OR h.id < $5)
	ORDER BY h.id DESC
	LIMIT $6`, appID, eventSource, eventID, subscriptionID, before.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]EventFanoutAttempt, 0)
	for rows.Next() {
		var attempt EventFanoutAttempt
		if err := rows.Scan(&attempt.ID, &attempt.OutboxID, &attempt.AppID,
			&attempt.EventID, &attempt.EventSource, &attempt.EventType, &attempt.SubscriptionID,
			&attempt.Action, &attempt.State, &attempt.Attempts, &attempt.FailureCode,
			&attempt.Retryable, &attempt.LastError, &attempt.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, attempt)
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
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin published event recipient replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	var outboxID int64
	var recipientAppID string
	var priorJSON []byte
	err = tx.QueryRow(ctx, `SELECT o.id, r.recipient->>'app_id', o.recipient_progress -> $5
		FROM event_fanout_outbox AS o
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		WHERE o.account_id = $1::uuid AND o.source = $3 AND o.event_id = $4
		  AND o.state = 'delivered'
		  AND (o.recipient_progress -> $5)->>'state' = 'failed'
		  AND r.recipient->>'app_id' = $2::text AND r.recipient->>'id' = $5
		FOR UPDATE OF o`, accountID, appID, eventSource, eventID, subscriptionID).Scan(&outboxID, &recipientAppID, &priorJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		var failed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (
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
	if err != nil {
		return fmt.Errorf("load published event recipient replay: %w", err)
	}
	var previous PublishedEventRecipientProgress
	if err := json.Unmarshal(priorJSON, &previous); err != nil {
		return fmt.Errorf("decode published event recipient before replay: %w", err)
	}
	now := time.Now().UTC()
	result, err := tx.Exec(ctx, `UPDATE event_fanout_outbox AS o
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
		WHERE o.id = $6 AND o.account_id = $1::uuid AND o.source = $3 AND o.event_id = $4
		  AND o.state = 'delivered'
		  AND (o.recipient_progress -> $5)->>'state' = 'failed'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		              WHERE r.recipient->>'app_id' = $2::text AND r.recipient->>'id' = $5)`,
		accountID, appID, eventSource, eventID, subscriptionID, outboxID)
	if err != nil {
		return fmt.Errorf("replay published event recipient: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO event_fanout_attempt_history
		(outbox_id, app_id, subscription_id, action, state, attempts,
		 failure_code, retryable, last_error, occurred_at)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10)`,
		outboxID, recipientAppID, subscriptionID, EventFanoutAttemptActionReplay,
		PublishedEventRecipientPending, previous.Attempts, previous.FailureCode,
		previous.Retryable, previous.LastError, now); err != nil {
		return fmt.Errorf("append published event recipient replay: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit published event recipient replay: %w", err)
	}
	return nil
}

// ReplayRetryablePublishedEventRecipientsForApp requeues the oldest bounded
// set of retryable terminal recipients. Candidates are tied to the app's
// immutable recipient snapshot. Pending receipts can accept additional replay
// requests; processing receipts remain untouched until their claim settles.
func (s *PgStore) ReplayRetryablePublishedEventRecipientsForApp(ctx context.Context, accountID, appID, eventSource, eventID string, limit int) (EventFanoutReplayBatch, error) {
	limit = normalizeEventFanoutReplayLimit(limit)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EventFanoutReplayBatch{}, fmt.Errorf("begin event fanout replay batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	rows, err := tx.Query(ctx, `SELECT o.id, p.key, r.recipient->>'app_id', p.outcome
		FROM event_fanout_outbox AS o
		JOIN apps AS a ON a.id = $2::uuid AND a.account_id = o.account_id
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		CROSS JOIN LATERAL jsonb_each(COALESCE(o.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
		WHERE o.account_id = $1::uuid AND o.state IN ('delivered', 'pending')
		  AND r.recipient->>'app_id' = a.id::text AND r.recipient->>'id' = p.key
		  AND p.outcome->>'state' = 'failed'
		  AND COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false)
		  AND (($3 = '' AND $4 = '') OR (o.source = $3 AND o.event_id = $4))
		ORDER BY COALESCE(NULLIF(p.outcome->>'updated_at', '')::timestamptz, o.created_at), o.id, p.key
		LIMIT $5 FOR UPDATE OF o SKIP LOCKED`, accountID, appID, eventSource, eventID, limit)
	if err != nil {
		return EventFanoutReplayBatch{}, fmt.Errorf("list retryable event fanout failures: %w", err)
	}
	type candidate struct {
		outboxID       int64
		subscriptionID string
		appID          string
		progress       PublishedEventRecipientProgress
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		var progressJSON []byte
		if err := rows.Scan(&item.outboxID, &item.subscriptionID, &item.appID, &progressJSON); err != nil {
			rows.Close()
			return EventFanoutReplayBatch{}, fmt.Errorf("scan retryable event fanout failure: %w", err)
		}
		if err := json.Unmarshal(progressJSON, &item.progress); err != nil {
			rows.Close()
			return EventFanoutReplayBatch{}, fmt.Errorf("decode retryable event fanout failure: %w", err)
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return EventFanoutReplayBatch{}, fmt.Errorf("read retryable event fanout failures: %w", err)
	}
	result := EventFanoutReplayBatch{}
	now := time.Now().UTC()
	for _, item := range candidates {
		if _, err := tx.Exec(ctx, `INSERT INTO event_fanout_attempt_history
			(outbox_id, app_id, subscription_id, action, state, attempts,
			 failure_code, retryable, last_error, occurred_at)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10)`,
			item.outboxID, item.appID, item.subscriptionID, EventFanoutAttemptActionReplay,
			PublishedEventRecipientPending, item.progress.Attempts, item.progress.FailureCode,
			item.progress.Retryable, item.progress.LastError, now); err != nil {
			return EventFanoutReplayBatch{}, fmt.Errorf("append retryable event recipient replay: %w", err)
		}
		tag, err := tx.Exec(ctx, `UPDATE event_fanout_outbox AS o
			SET state = 'pending', available_at = $3::timestamptz, delivered_at = NULL,
			    claim_token = NULL, lease_until = NULL,
			    recipient_progress = jsonb_set(o.recipient_progress, ARRAY[$2::text],
			        ((o.recipient_progress -> $2) - 'failure_code' - 'retryable' - 'last_error') ||
			            jsonb_build_object('state', 'pending', 'updated_at', $3::timestamptz), false),
			    last_error = (SELECT left('subscription ' || progress.key || ': ' ||
			        coalesce(progress.outcome->>'last_error', 'recipient failed'), 1024)
			        FROM jsonb_each(o.recipient_progress) AS progress(key, outcome)
			        WHERE progress.key <> $2 AND progress.outcome->>'state' = 'failed'
			        ORDER BY progress.key LIMIT 1)
			WHERE o.id = $1 AND o.state IN ('delivered', 'pending')
			  AND (o.recipient_progress -> $2)->>'state' = 'failed'
			  AND COALESCE(NULLIF((o.recipient_progress -> $2)->>'retryable', '')::boolean, false)`,
			item.outboxID, item.subscriptionID, now)
		if err != nil {
			return EventFanoutReplayBatch{}, fmt.Errorf("requeue retryable event recipient %s: %w", item.subscriptionID, err)
		}
		if tag.RowsAffected() == 0 {
			return EventFanoutReplayBatch{}, fmt.Errorf("requeue retryable event recipient %s: %w", item.subscriptionID, ErrConflict)
		}
		result.Replayed++
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM event_fanout_outbox AS o
		JOIN apps AS a ON a.id = $2::uuid AND a.account_id = o.account_id
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(o.recipient_snapshot, '[]'::jsonb)) AS r(recipient)
		CROSS JOIN LATERAL jsonb_each(COALESCE(o.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
		WHERE o.account_id = $1::uuid AND o.state IN ('delivered', 'pending', 'processing')
		  AND r.recipient->>'app_id' = a.id::text AND r.recipient->>'id' = p.key
		  AND p.outcome->>'state' = 'failed'
		  AND COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false)
		  AND (($3 = '' AND $4 = '') OR (o.source = $3 AND o.event_id = $4))
	)`, accountID, appID, eventSource, eventID).Scan(&result.HasMore); err != nil {
		return EventFanoutReplayBatch{}, fmt.Errorf("check remaining retryable event fanout failures: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EventFanoutReplayBatch{}, fmt.Errorf("commit event fanout replay batch: %w", err)
	}
	return result, nil
}

func normalizeEventFanoutReplayLimit(limit int) int {
	if limit <= 0 {
		return EventFanoutReplayBatchMax
	}
	if limit > EventFanoutReplayBatchMax {
		return EventFanoutReplayBatchMax
	}
	return limit
}

type publishedEventIdentity struct {
	Source        string          `json:"source"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SchemaVersion string          `json:"schemaversion"`
	Data          json.RawMessage `json:"data"`
}

// appendEventFanoutAttemptLocked mirrors the durable attempt-history insert.
// Callers hold m.mu while changing the corresponding recipient checkpoint.
func (m *MemStore) appendEventFanoutAttemptLocked(work *PublishedEventWork, recipientID, action string, progress PublishedEventRecipientProgress) {
	var event publishedEventIdentity
	if json.Unmarshal(work.Payload, &event) != nil {
		return
	}
	var appID string
	for _, recipient := range work.RecipientSnapshot {
		if recipient.ID == recipientID {
			appID = recipient.AppID
			break
		}
	}
	if appID == "" {
		return
	}
	if m.eventFanoutAttemptNextID == 0 {
		for _, attempt := range m.eventFanoutAttempts {
			if attempt.ID > m.eventFanoutAttemptNextID {
				m.eventFanoutAttemptNextID = attempt.ID
			}
		}
	}
	m.eventFanoutAttemptNextID++
	m.eventFanoutAttempts = append(m.eventFanoutAttempts, EventFanoutAttempt{
		ID: m.eventFanoutAttemptNextID, OutboxID: work.ID, AppID: appID,
		EventID: event.ID, EventSource: event.Source, EventType: event.Type,
		SubscriptionID: recipientID, Action: action, State: progress.State,
		Attempts: progress.Attempts, FailureCode: progress.FailureCode,
		Retryable: progress.Retryable, LastError: progress.LastError,
		OccurredAt: progress.UpdatedAt,
	})
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
		recipient := PublishedEventRecipient{ID: row.ID, AccountID: row.AccountID,
			AppID: row.AppID, Source: row.Source, Type: row.Type, Filter: bytes.Clone(row.Filter),
			WorkSnapshotCaptured: true}
		if binding, ok := m.eventWorkBindings[row.ID]; ok {
			recipient.Work = &PublishedEventWorkBindingSnapshot{
				PolicyName: binding.PolicyName, KeySelector: binding.KeySelector,
				FairnessSelector: binding.FairnessSelector, Action: binding.Action,
			}
			if policy, exists := m.workPolicies[memWorkPolicyKey(row.AppID, binding.PolicyName)]; exists {
				recipient.Work.Policy = snapshotPublishedEventWorkPolicy(policy)
			}
		}
		recipients = append(recipients, recipient)
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
		if recipient.ObjectNotification != nil {
			n := cloneObjectNotificationSnapshot(*recipient.ObjectNotification)
			copy.RecipientSnapshot[i].ObjectNotification = &n
		}
		if recipient.Work != nil {
			binding := *recipient.Work
			if recipient.Work.Policy != nil {
				policy := *recipient.Work.Policy
				binding.Policy = &policy
			}
			copy.RecipientSnapshot[i].Work = &binding
		}
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
		m.appendEventFanoutAttemptLocked(work, recipientID, EventFanoutAttemptActionAttempt, progress)
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
	if pruned > 0 {
		liveOutboxIDs := make(map[int64]struct{}, len(m.eventFanout))
		for _, work := range m.eventFanout {
			liveOutboxIDs[work.ID] = struct{}{}
		}
		attempts := m.eventFanoutAttempts[:0]
		for _, attempt := range m.eventFanoutAttempts {
			if _, ok := liveOutboxIDs[attempt.OutboxID]; ok {
				attempts = append(attempts, attempt)
			}
		}
		m.eventFanoutAttempts = attempts
	}
	return pruned, nil
}

// ListEventFanoutFailuresForApp mirrors the PostgreSQL projection for tests
// and in-memory API use.
func (m *MemStore) ListEventFanoutFailuresForApp(_ context.Context, appID string, limit int, before EventFanoutFailureCursor, eventSource, eventID string) ([]EventFanoutFailure, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]EventFanoutFailure, 0)
	for _, work := range m.eventFanout {
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil ||
			(eventSource != "" && event.Source != eventSource) || (eventID != "" && event.ID != eventID) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AppID, appID) {
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
			failureCode := progress.FailureCode
			if failureCode == "" {
				failureCode = EventFanoutFailureCodeUnknown
			}
			out = append(out, EventFanoutFailure{
				OutboxID: work.ID, EventID: event.ID, EventSource: event.Source, EventType: event.Type,
				SubscriptionID: recipient.ID, Attempts: progress.Attempts,
				FailureCode: failureCode, Retryable: progress.Retryable, LastError: progress.LastError,
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

// ListEventFanoutAttemptsForApp mirrors the PostgreSQL attempt-history
// projection for API and state tests.
func (m *MemStore) ListEventFanoutAttemptsForApp(_ context.Context, appID string, limit int, before EventFanoutAttemptCursor, eventSource, eventID, subscriptionID string) ([]EventFanoutAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]EventFanoutAttempt, 0)
	for _, attempt := range m.eventFanoutAttempts {
		if !sameMemUUID(attempt.AppID, appID) ||
			(eventSource != "" && attempt.EventSource != eventSource) ||
			(eventID != "" && attempt.EventID != eventID) ||
			(subscriptionID != "" && attempt.SubscriptionID != subscriptionID) ||
			(before.ID > 0 && attempt.ID >= before.ID) {
			continue
		}
		out = append(out, attempt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
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
			if recipient.ID == subscriptionID && sameMemUUID(recipient.AppID, appID) && sameMemUUID(recipient.AccountID, accountID) {
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
		replay := progress
		replay.State = PublishedEventRecipientPending
		replay.UpdatedAt = time.Now().UTC()
		m.appendEventFanoutAttemptLocked(work, subscriptionID, EventFanoutAttemptActionReplay, replay)
		progress.State = PublishedEventRecipientPending
		progress.FailureCode = ""
		progress.Retryable = false
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

func (m *MemStore) ReplayRetryablePublishedEventRecipientsForApp(_ context.Context, accountID, appID, eventSource, eventID string, limit int) (EventFanoutReplayBatch, error) {
	limit = normalizeEventFanoutReplayLimit(limit)
	type candidate struct {
		work           *PublishedEventWork
		subscriptionID string
		failedAt       time.Time
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := make([]candidate, 0)
	for _, work := range m.eventFanout {
		if !work.Delivered && work.ClaimToken != "" {
			continue
		}
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil ||
			((eventSource != "" || eventID != "") && (event.Source != eventSource || event.ID != eventID)) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AccountID, accountID) || !sameMemUUID(recipient.AppID, appID) {
				continue
			}
			progress, ok := work.RecipientProgress[recipient.ID]
			if !ok || progress.State != PublishedEventRecipientFailed || !progress.Retryable {
				continue
			}
			candidates = append(candidates, candidate{work: work, subscriptionID: recipient.ID, failedAt: progress.UpdatedAt})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].failedAt.Equal(candidates[j].failedAt) {
			return candidates[i].failedAt.Before(candidates[j].failedAt)
		}
		if candidates[i].work.ID != candidates[j].work.ID {
			return candidates[i].work.ID < candidates[j].work.ID
		}
		return candidates[i].subscriptionID < candidates[j].subscriptionID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result := EventFanoutReplayBatch{}
	now := time.Now().UTC()
	for _, item := range candidates {
		progress := item.work.RecipientProgress[item.subscriptionID]
		replay := progress
		replay.State = PublishedEventRecipientPending
		replay.UpdatedAt = now
		m.appendEventFanoutAttemptLocked(item.work, item.subscriptionID, EventFanoutAttemptActionReplay, replay)
		progress.State = PublishedEventRecipientPending
		progress.FailureCode = ""
		progress.Retryable = false
		progress.LastError = ""
		progress.UpdatedAt = now
		item.work.RecipientProgress[item.subscriptionID] = progress
		item.work.Delivered = false
		item.work.DeliveredAt = time.Time{}
		item.work.AvailableAt = now
		item.work.ClaimToken = ""
		item.work.LeaseUntil = time.Time{}
		result.Replayed++
	}
	for _, work := range m.eventFanout {
		var event publishedEventIdentity
		if json.Unmarshal(work.Payload, &event) != nil ||
			((eventSource != "" || eventID != "") && (event.Source != eventSource || event.ID != eventID)) {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if !sameMemUUID(recipient.AccountID, accountID) || !sameMemUUID(recipient.AppID, appID) {
				continue
			}
			progress, ok := work.RecipientProgress[recipient.ID]
			if ok && progress.State == PublishedEventRecipientFailed && progress.Retryable {
				result.HasMore = true
				break
			}
		}
		if result.HasMore {
			break
		}
	}
	return result, nil
}
