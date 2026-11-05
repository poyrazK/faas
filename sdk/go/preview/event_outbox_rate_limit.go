package preview

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"
)

// EventOutboxRateLimitSchema is opt-in application-owned DDL. Install before
// upgrading recovery and health readers; no helper executes schema changes.
//
//go:embed event_outbox_rate_limit_schema.sql
var EventOutboxRateLimitSchema string

var ErrEventOutboxRateLimited = errors.New("event outbox publishing is rate limited")

// EventOutboxRateLimitWaitError reports database-clock capacity evidence. It is
// not a reservation and can be stale when another worker consumes capacity.
type EventOutboxRateLimitWaitError struct {
	RetryAfter time.Duration
	RetryAt    time.Time
}

func (e *EventOutboxRateLimitWaitError) Error() string { return ErrEventOutboxRateLimited.Error() }
func (e *EventOutboxRateLimitWaitError) Unwrap() error { return ErrEventOutboxRateLimited }

type EventOutboxRateLimitStatus struct {
	AccountID           string     `json:"account_id"`
	AppID               string     `json:"app_id"`
	Version             int        `json:"version"`
	Enabled             bool       `json:"enabled"`
	Revision            int64      `json:"revision"`
	IntervalMillis      int64      `json:"interval_millis"`
	Burst               int64      `json:"burst"`
	AvailableTokens     int64      `json:"available_tokens"`
	RateLimited         bool       `json:"rate_limited"`
	NextAvailableAt     *time.Time `json:"next_available_at,omitempty"`
	ObservedAt          time.Time  `json:"observed_at"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
	LastCommandSequence int64      `json:"last_command_sequence"`
}

const eventOutboxRateLimitColumns = `version,enabled,revision,interval_ms,burst,credit_ms,refilled_at,updated_at,last_sequence,clock_timestamp()`

type eventOutboxRateBucket struct {
	status EventOutboxRateLimitStatus
	credit int64
	anchor time.Time
}

func scanEventOutboxRateBucket(row eventIntentScanner, account, app string) (eventOutboxRateBucket, error) {
	b := eventOutboxRateBucket{status: EventOutboxRateLimitStatus{AccountID: account, AppID: app}}
	var updated sql.NullTime
	s := &b.status
	err := row.Scan(&s.Version, &s.Enabled, &s.Revision, &s.IntervalMillis, &s.Burst, &b.credit, &b.anchor, &updated, &s.LastCommandSequence, &s.ObservedAt)
	if err != nil {
		return b, err
	}
	if s.Version != 1 || s.Revision < 0 || s.IntervalMillis < 1 || s.IntervalMillis > 3600000 || s.Burst < 1 || s.Burst > 10000 || b.credit < 0 || b.credit > s.IntervalMillis*s.Burst || b.anchor.After(s.ObservedAt) || (updated.Valid && updated.Time.After(s.ObservedAt)) {
		return b, ErrEventOutboxStore
	}
	if updated.Valid {
		s.UpdatedAt = &updated.Time
	}
	capacity := s.IntervalMillis * s.Burst
	elapsed := min(s.ObservedAt.Sub(b.anchor).Milliseconds(), capacity)
	b.credit = min(capacity, b.credit+elapsed)
	if b.credit == capacity {
		b.anchor = s.ObservedAt
	} else {
		b.anchor = b.anchor.Add(time.Duration(elapsed) * time.Millisecond)
	}
	b.describe()
	return b, nil
}
func (b *eventOutboxRateBucket) describe() {
	b.status.AvailableTokens = 0
	b.status.RateLimited = false
	b.status.NextAvailableAt = nil
	if !b.status.Enabled {
		return
	}
	b.status.AvailableTokens = b.credit / b.status.IntervalMillis
	b.status.RateLimited = b.status.AvailableTokens == 0
	if b.status.RateLimited {
		next := b.anchor.Add(time.Duration(b.status.IntervalMillis-b.credit) * time.Millisecond)
		b.status.NextAvailableAt = &next
	}
}

// GetEventOutboxRateLimit reads projected capacity without reserving or refilling
// storage. Missing configuration means disabled; missing storage is an error.
func GetEventOutboxRateLimit(ctx context.Context, db *sql.DB, accountID, appID string) (EventOutboxRateLimitStatus, error) {
	var zero EventOutboxRateLimitStatus
	if ctx == nil || db == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	b, err := scanEventOutboxRateBucket(db.QueryRowContext(ctx, `WITH observed AS MATERIALIZED (SELECT clock_timestamp() AS t) SELECT coalesce(r.version,1),coalesce(r.enabled,false),coalesce(r.revision,0)::bigint,coalesce(r.interval_ms,1000)::bigint,coalesce(r.burst,1)::bigint,coalesce(r.credit_ms,1000)::bigint,coalesce(r.refilled_at,o.t),r.updated_at,coalesce(r.last_sequence,0)::bigint,o.t FROM observed o LEFT JOIN public.gregale_event_outbox_rate_limits r ON r.account_id=$1::uuid AND r.app_id=$2::uuid`, account, app), account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return b.status, nil
}

// The caller already holds the publishing shared lock. Hold bucket state only
// through claim commit, not HTTP. Empty/blocked/failed claims never debit tokens.
func claimEventOutboxRateBucket(ctx context.Context, tx *sql.Tx, account, app string) (*eventOutboxRateBucket, error) {
	b, err := scanEventOutboxRateBucket(tx.QueryRowContext(ctx, `SELECT `+eventOutboxRateLimitColumns+` FROM public.gregale_event_outbox_rate_limits WHERE account_id=$1::uuid AND app_id=$2::uuid FOR UPDATE`, account, app), account, app)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, eventOutboxStoreError(ctx)
	}
	if !b.status.Enabled {
		return nil, nil
	}
	if b.status.RateLimited {
		return nil, &EventOutboxRateLimitWaitError{RetryAfter: b.status.NextAvailableAt.Sub(b.status.ObservedAt), RetryAt: *b.status.NextAvailableAt}
	}
	return &b, nil
}
func (b *eventOutboxRateBucket) consume(ctx context.Context, tx *sql.Tx, account, app string) error {
	if b == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE public.gregale_event_outbox_rate_limits SET credit_ms=$3::bigint,refilled_at=$4::timestamptz WHERE account_id=$1::uuid AND app_id=$2::uuid`, account, app, b.credit-b.status.IntervalMillis, b.anchor)
	if err != nil {
		return eventOutboxStoreError(ctx)
	}
	return nil
}
