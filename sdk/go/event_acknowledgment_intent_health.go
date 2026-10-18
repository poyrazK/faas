package faas

import (
	"context"
	"database/sql"
	"time"
)

// A scan is bounded to unfinished intents; terminal history does not crowd out work.
type EventAcknowledgmentIntentHealthOptions struct {
	ScanLimit  int
	StallAfter time.Duration
}

// Health describes retained local callback recovery, not business delivery success.
// Counts are lower bounds when Complete is false. No capabilities or payloads appear.
type EventAcknowledgmentIntentHealth struct {
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
	Parked                      int64      `json:"parked"`
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

func eventIntentHealthOptions(o EventAcknowledgmentIntentHealthOptions) (EventAcknowledgmentIntentHealthOptions, error) {
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

// GetEventAcknowledgmentIntentHealth reads one database snapshot and clock, sampling
// at most ScanLimit+1 unfinished rows. Expired claims are eligible only after both
// their due time and lease expiry. No work is claimed or mutated by this read.
// Parked work remains outstanding and is counted separately from eligibility.
func GetEventAcknowledgmentIntentHealth(ctx context.Context, db *sql.DB, accountID, appID string, options EventAcknowledgmentIntentHealthOptions) (EventAcknowledgmentIntentHealth, error) {
	var out EventAcknowledgmentIntentHealth
	if ctx == nil || db == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	options, err = eventIntentHealthOptions(options)
	if err != nil {
		return out, err
	}
	var oldest, eligible sql.NullTime
	err = db.QueryRowContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS observed_at), sample AS MATERIALIZED (
 SELECT state,next_attempt_at,claim_expires_at,created_at,updated_at,invocation_id,replay_generation,attempt
 FROM public.gregale_event_acknowledgment_intents
 WHERE account_id=$1::uuid AND app_id=$2::uuid AND state IN ('pending','unconfirmed','parked')
 ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT $3::integer+1
 ), classified AS (
 SELECT *,greatest(created_at,next_attempt_at,coalesce(claim_expires_at,next_attempt_at)) AS eligible_at
 FROM sample ORDER BY next_attempt_at,invocation_id,replay_generation,attempt LIMIT $3::integer
 ) SELECT c.observed_at,(SELECT count(*) FROM sample)>$3::integer,
 count(s.state)::bigint,
 count(*) FILTER(WHERE s.state='pending')::bigint,
 count(*) FILTER(WHERE s.state='unconfirmed')::bigint,
 count(*) FILTER(WHERE s.state='parked')::bigint,
 count(*) FILTER(WHERE s.state<>'parked' AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.state<>'parked' AND s.claim_expires_at>c.observed_at)::bigint,
 count(*) FILTER(WHERE s.state<>'parked' AND s.eligible_at>c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.state<>'parked' AND s.claim_expires_at<=c.observed_at)::bigint,
 count(*) FILTER(WHERE s.state<>'parked' AND s.eligible_at<=c.observed_at-$4::double precision*interval '1 second' AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::bigint,
 count(*) FILTER(WHERE s.created_at>c.observed_at OR s.updated_at>c.observed_at OR s.updated_at<s.created_at OR NOT isfinite(s.created_at) OR NOT isfinite(s.updated_at) OR NOT isfinite(s.next_attempt_at) OR (s.claim_expires_at IS NOT NULL AND NOT isfinite(s.claim_expires_at)))::bigint,
 min(s.created_at) FILTER(WHERE isfinite(s.created_at))::timestamptz,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.created_at) FILTER(WHERE isfinite(s.created_at)))),0)::double precision,
 min(s.eligible_at) FILTER(WHERE s.state<>'parked' AND isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at))::timestamptz,
 coalesce(greatest(0,extract(epoch FROM c.observed_at-min(s.eligible_at) FILTER(WHERE s.state<>'parked' AND isfinite(s.eligible_at) AND s.eligible_at<=c.observed_at AND (s.claim_expires_at IS NULL OR s.claim_expires_at<=c.observed_at)))),0)::double precision
 FROM clock c LEFT JOIN classified s ON true GROUP BY c.observed_at`, account, app, options.ScanLimit, options.StallAfter.Seconds()).Scan(&out.ObservedAt, &out.Truncated, &out.Outstanding, &out.Pending, &out.Unconfirmed, &out.Parked, &out.Due, &out.Leased, &out.Scheduled, &out.ExpiredClaims, &out.Stalled, &out.InvalidRows, &oldest, &out.OldestOutstandingAgeSeconds, &eligible, &out.OldestOverdueAgeSeconds)
	if err != nil {
		return EventAcknowledgmentIntentHealth{}, eventIntentStoreError(ctx)
	}
	out.AccountID, out.AppID = account, app
	out.Coverage = "bounded_retained_unfinished_intents"
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
