package faas

import (
	"context"
	"database/sql"
	"time"
)

// A scan is bounded to unpublished rows; published history does not crowd out work.
type EventOutboxHealthOptions struct {
	ScanLimit  int
	StallAfter time.Duration
}

// Health describes retained local publication recovery, not consumer completion.
// Counts are lower bounds when Complete is false. No capabilities or payloads appear.
type EventOutboxHealth struct {
	AccountID                   string     `json:"account_id"`
	AppID                       string     `json:"app_id"`
	ObservedAt                  time.Time  `json:"observed_at"`
	Coverage                    string     `json:"coverage"`
	Complete                    bool       `json:"complete"`
	Truncated                   bool       `json:"truncated"`
	ScanLimit                   int        `json:"scan_limit"`
	InvalidRows                 int64      `json:"invalid_rows"`
	Outstanding                 int64      `json:"outstanding"`
	Pending                     int64      `json:"pending"`
	Unconfirmed                 int64      `json:"unconfirmed"`
	Blocked                     int64      `json:"blocked"`
	Expired                     int64      `json:"expired"`
	Due                         int64      `json:"due"`
	Leased                      int64      `json:"leased"`
	Scheduled                   int64      `json:"scheduled"`
	ExpiredClaims               int64      `json:"expired_claims"`
	Stalled                     int64      `json:"stalled"`
	StallAfterSeconds           float64    `json:"stall_after_seconds"`
	OldestOutstandingAt         *time.Time `json:"oldest_outstanding_at,omitempty"`
	OldestOutstandingAgeSeconds float64    `json:"oldest_outstanding_age_seconds"`
	OldestEligibleAt            *time.Time `json:"oldest_eligible_at,omitempty"`
	OldestOverdueAgeSeconds     float64    `json:"oldest_overdue_age_seconds"`
}

func eventOutboxHealthOptions(o EventOutboxHealthOptions) (EventOutboxHealthOptions, error) {
	if o.ScanLimit == 0 {
		o.ScanLimit = 1000
	}
	if o.StallAfter == 0 {
		o.StallAfter = 5 * time.Minute
	}
	if o.ScanLimit < 1 || o.ScanLimit > 10000 || o.StallAfter < time.Second || o.StallAfter > 24*time.Hour {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}

// GetEventOutboxHealth reads one database snapshot and clock, sampling
// at most ScanLimit+1 unpublished rows. Due/stalled work excludes blocked or
// expired publications and live claims. Eligibility respects next-attempt time
// and lease expiry. This read never claims, mutates, or publishes work.
func GetEventOutboxHealth(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxHealthOptions) (EventOutboxHealth, error) {
	var out EventOutboxHealth
	if ctx == nil || db == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	options, err = eventOutboxHealthOptions(options)
	if err != nil {
		return out, err
	}
	var oldest, eligible sql.NullTime
	err = db.QueryRowContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at), sample AS MATERIALIZED (
 SELECT state,next_attempt_at,claim_expires_at,created_at,updated_at,retry_until,producer_key
 FROM public.gregale_event_outbox
 WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed','blocked')
 ORDER BY created_at,producer_key COLLATE "C" LIMIT $3::integer+1
 ), classified AS (
 SELECT *,greatest(created_at,next_attempt_at,coalesce(claim_expires_at,next_attempt_at)) AS eligible_at
 FROM sample ORDER BY created_at,producer_key COLLATE "C" LIMIT $3::integer
 ) SELECT c.observed_at,(SELECT count(*) FROM sample)>$3::integer,
 count(s.state)::bigint,
 count(*) FILTER(WHERE s.state='pending')::bigint,
 count(*) FILTER(WHERE s.state='unconfirmed')::bigint,
 count(*) FILTER(WHERE s.state='blocked')::bigint,
 count(*) FILTER(WHERE s.retry_until<=c.observed_at)::bigint,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.claim_expires_at>c.observed_at)::bigint,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.eligible_at>c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.claim_expires_at<=c.observed_at)::bigint,
 count(*) FILTER(WHERE s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.eligible_at<=c.observed_at-$4::double precision*interval '1 second' AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.created_at>c.observed_at OR s.updated_at>c.observed_at OR s.updated_at<s.created_at OR NOT isfinite(s.created_at) OR NOT isfinite(s.updated_at) OR s.retry_until<=s.created_at OR NOT isfinite(s.retry_until) OR NOT isfinite(s.next_attempt_at) OR (s.claim_expires_at IS NOT NULL AND (NOT isfinite(s.claim_expires_at) OR s.claim_expires_at>s.retry_until OR s.claim_expires_at<s.created_at)))::bigint,
 min(s.created_at) FILTER(WHERE isfinite(s.created_at))::timestamptz,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.created_at) FILTER(WHERE isfinite(s.created_at)))),0)::double precision,
 min(s.eligible_at) FILTER(WHERE isfinite(s.eligible_at) AND s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::timestamptz,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.eligible_at) FILTER(WHERE isfinite(s.eligible_at) AND s.state IN ('pending','unconfirmed') AND s.retry_until>c.observed_at AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at)))),0)::double precision
 FROM clock c LEFT JOIN classified s ON true GROUP BY c.observed_at`, account, app, options.ScanLimit, options.StallAfter.Seconds()).Scan(&out.ObservedAt, &out.Truncated, &out.Outstanding, &out.Pending, &out.Unconfirmed, &out.Blocked, &out.Expired, &out.Due, &out.Leased, &out.Scheduled, &out.ExpiredClaims, &out.Stalled, &out.InvalidRows, &oldest, &out.OldestOutstandingAgeSeconds, &eligible, &out.OldestOverdueAgeSeconds)
	if err != nil {
		return EventOutboxHealth{}, eventOutboxStoreError(ctx)
	}
	out.AccountID, out.AppID = account, app
	out.Coverage = "bounded_retained_unpublished_outbox"
	out.ScanLimit = options.ScanLimit
	out.StallAfterSeconds = options.StallAfter.Seconds()
	out.Complete = !out.Truncated && out.InvalidRows == 0
	if oldest.Valid {
		out.OldestOutstandingAt = &oldest.Time
	}
	if eligible.Valid {
		out.OldestEligibleAt = &eligible.Time
	}
	return out, nil
}
