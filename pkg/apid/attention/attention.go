// Package attention finds customers who are probably stuck and keeps one
// signal per account, kind and subject for support (ADR-20261113123000,
// round 2 ADR-20261113180000).
//
// An evaluation runs the rules against data the platform already stores
// (failed deployments, recorded API refusals, custom-domain status, runtime
// OOM kills, failed boots, scheduled job runs, payment receipts), opens or
// refreshes a signal for every finding, and resolves every active signal
// the rules no longer find. Support may contact the customer about an open
// signal or dismiss it with an outcome; either way the signal stays quiet
// until its condition clears, after which a recurrence opens a fresh one. A
// signal that clears with no outcome is marked resolved_on_own. Signals hold
// identifiers only.
package attention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// evaluateLockKey serializes evaluations across apid replicas
// (pg_try_advisory_xact_lock).
const evaluateLockKey int64 = 0x617474656e74696f // "attentio"

// ErrNotFound is returned by Dismiss and Contacted when no open signal has
// the id, and by Get when no signal has it.
var ErrNotFound = errors.New("attention: no open signal with this id")

// ErrInvalid wraps rejected dismiss input.
var ErrInvalid = errors.New("attention: invalid input")

// refusedBurstIgnored are problem codes that repeat in healthy use.
var refusedBurstIgnored = []string{api.CodeNotFound}

// limitCodePredicate selects plan-limit and quota refusals: the
// plan_limit_* family, *_quota_exceeded and *_limit_exceeded. Rate limits
// are left out; they are transient and refused_burst covers bursts.
const limitCodePredicate = `(code LIKE 'plan\_limit\_%' OR code LIKE '%\_quota\_exceeded' OR code LIKE '%\_limit\_exceeded')`

// Finding is one rule match before it is stored.
type Finding struct {
	AccountID, Kind, Subject, Detail string
	Observed                         int64
}

// Result counts what one evaluation changed. Skipped is true when another
// replica held the evaluation lock.
type Result struct {
	Opened, Refreshed, Resolved, Pruned int64
	OpenByKind                          map[string]int64
	// ResolvedOnOwnByKind counts open signals that cleared in this
	// evaluation with no support action.
	ResolvedOnOwnByKind map[string]int64
	Skipped             bool
}

// Evaluator evaluates the rules and serves signal reads and staff actions.
// A nil *Evaluator reads nothing and evaluates nothing (apid without
// PostgreSQL).
type Evaluator struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
	Log  *slog.Logger
	// Observe, when set, receives each evaluation's result (metrics).
	Observe func(Result)
}

// New returns an Evaluator for pool, or nil when pool is nil.
func New(pool *pgxpool.Pool, log *slog.Logger, observe func(Result)) *Evaluator {
	if pool == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Evaluator{Pool: pool, Now: time.Now, Log: log, Observe: observe}
}

// Run evaluates every api.AttentionEvaluateInterval until ctx ends.
func (e *Evaluator) Run(ctx context.Context) {
	if e == nil {
		return
	}
	ticker := time.NewTicker(api.AttentionEvaluateInterval)
	defer ticker.Stop()
	for {
		if _, err := e.Evaluate(ctx); err != nil && ctx.Err() == nil {
			e.Log.Warn("attention: evaluation failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Evaluate runs the rules once and stores the outcome in one transaction.
func (e *Evaluator) Evaluate(ctx context.Context) (Result, error) {
	if e == nil {
		return Result{}, nil
	}
	now := e.Now().UTC()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("attention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, evaluateLockKey).Scan(&locked); err != nil {
		return Result{}, fmt.Errorf("attention: lock: %w", err)
	}
	if !locked {
		return Result{Skipped: true}, nil
	}
	findings, err := detect(ctx, tx, now)
	if err != nil {
		return Result{}, err
	}
	result := Result{OpenByKind: map[string]int64{}, ResolvedOnOwnByKind: map[string]int64{}}
	for _, f := range findings {
		var inserted bool
		err := tx.QueryRow(ctx, `INSERT INTO customer_attention_signals
			(account_id, kind, subject, detail, state, observed, first_seen_at, last_seen_at)
			VALUES ($1, $2, $3, $4, 'open', $5, $6, $6)
			ON CONFLICT (account_id, kind, subject) WHERE state IN ('open','dismissed')
			DO UPDATE SET detail = EXCLUDED.detail, observed = EXCLUDED.observed, last_seen_at = EXCLUDED.last_seen_at
			RETURNING xmax = 0`, f.AccountID, f.Kind, f.Subject, f.Detail, f.Observed, now).Scan(&inserted)
		if err != nil {
			return Result{}, fmt.Errorf("attention: store %s %s: %w", f.Kind, f.Subject, err)
		}
		if inserted {
			result.Opened++
		} else {
			result.Refreshed++
		}
	}
	if err := resolve(ctx, tx, now, &result); err != nil {
		return Result{}, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM customer_attention_signals WHERE id IN (
		SELECT id FROM customer_attention_signals WHERE state = 'resolved' AND resolved_at < $1 ORDER BY resolved_at LIMIT 5000)`,
		now.Add(-api.AttentionResolvedRetention))
	if err != nil {
		return Result{}, fmt.Errorf("attention: prune: %w", err)
	}
	result.Pruned = tag.RowsAffected()
	if err := countByKind(ctx, tx, `SELECT kind, count(*) FROM customer_attention_signals WHERE state = 'open' GROUP BY kind`, result.OpenByKind); err != nil {
		return Result{}, fmt.Errorf("attention: count: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("attention: commit: %w", err)
	}
	if e.Observe != nil {
		e.Observe(result)
	}
	return result, nil
}

// resolve closes every active signal this evaluation did not see. An open
// one cleared with no support action, so it gets outcome resolved_on_own; a
// dismissed one keeps the outcome support gave it.
func resolve(ctx context.Context, tx pgx.Tx, now time.Time, result *Result) error {
	if err := countByKind(ctx, tx, `WITH resolved AS (
		UPDATE customer_attention_signals SET state = 'resolved', resolved_at = $1, outcome = 'resolved_on_own'
		WHERE state = 'open' AND last_seen_at < $1 RETURNING kind)
		SELECT kind, count(*) FROM resolved GROUP BY kind`, result.ResolvedOnOwnByKind, now); err != nil {
		return fmt.Errorf("attention: resolve: %w", err)
	}
	for _, n := range result.ResolvedOnOwnByKind {
		result.Resolved += n
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_attention_signals SET state = 'resolved', resolved_at = $1
		WHERE state = 'dismissed' AND last_seen_at < $1`, now)
	if err != nil {
		return fmt.Errorf("attention: resolve: %w", err)
	}
	result.Resolved += tag.RowsAffected()
	return nil
}

func countByKind(ctx context.Context, tx pgx.Tx, query string, into map[string]int64, args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			return err
		}
		into[kind] += n
	}
	return rows.Err()
}

// rule is one detection query. Its arguments are the window start, the
// threshold (when it has one) and the kind, in the order the query numbers
// them.
type rule struct {
	kind, query string
	args        []any
}

func rules(now time.Time) []rule {
	return []rule{
		{api.AttentionKindDeployFailing, `
		WITH failed AS (
			SELECT d.app_id, count(*) AS n, min(d.created_at) AS first_failed,
			       (array_agg(coalesce(nullif(d.error_code, ''), left(coalesce(d.error, ''), 200)) ORDER BY d.created_at DESC))[1] AS last_error
			FROM deployments d
			WHERE d.status = 'failed' AND d.deleted_at IS NULL AND d.created_at >= $1
			GROUP BY d.app_id HAVING count(*) >= $2)
		SELECT a.account_id::text, $3::text, a.slug, coalesce(f.last_error, ''), f.n
		FROM failed f JOIN apps a ON a.id = f.app_id
		WHERE a.status <> 'deleted' AND NOT EXISTS (
			SELECT 1 FROM deployments s WHERE s.app_id = f.app_id AND s.deleted_at IS NULL
			  AND s.status IN ('live', 'superseded') AND s.created_at > f.first_failed)
		ORDER BY f.n DESC LIMIT 1000`,
			[]any{now.Add(-api.AttentionDeployWindow), api.AttentionDeployFailures}},
		{api.AttentionKindRefusedBurst, `
		SELECT account_id::text, $3::text, code, '', count(*)
		FROM customer_api_errors
		WHERE occurred_at >= $1 AND NOT (code = ANY($4))
		GROUP BY account_id, code HAVING count(*) >= $2
		ORDER BY count(*) DESC LIMIT 1000`,
			[]any{now.Add(-api.AttentionRefusedWindow), api.AttentionRefusedBurst}},
		{api.AttentionKindDomainStuck, `
		SELECT a.account_id::text, $3::text, d.domain::text,
		       CASE
		         WHEN d.cert_status = 'dns_drifted' THEN 'DNS no longer points at Gregale'
		         WHEN d.cert_status = 'failed' THEN 'certificate issuance failed: ' || left(coalesce(d.cert_last_error, ''), 300)
		         ELSE 'domain not verified'
		       END, 1::bigint
		FROM custom_domains d JOIN apps a ON a.id = d.app_id
		WHERE a.status <> 'deleted' AND (
		      d.cert_status = 'dns_drifted'
		   OR (d.cert_status = 'failed' AND d.cert_failed_at < $1)
		   OR (d.verified_at IS NULL AND d.verification_expires_at - interval '7 days' < $2))
		LIMIT 1000`,
			[]any{now.Add(-api.AttentionDomainFailedAfter), now.Add(-api.AttentionDomainUnverifiedAfter)}},
		// Runtime OOM kills: schedd emits one event per kill, live or not.
		{api.AttentionKindAppOOM, `
		SELECT a.account_id::text, $3::text, a.slug,
		       'OOM-killed ' || count(*) || ' times; peak '
		       || coalesce(max(CASE WHEN e.data->>'peak_mb' ~ '^[0-9]{1,9}$' THEN (e.data->>'peak_mb')::bigint END), 0)
		       || ' MB of a '
		       || coalesce(max(CASE WHEN e.data->>'plan_mb' ~ '^[0-9]{1,9}$' THEN (e.data->>'plan_mb')::bigint END), 0)
		       || ' MB plan',
		       count(*)
		FROM events e JOIN apps a ON a.id::text = e.data->>'app_id'
		WHERE e.kind = 'instances.workload_oom_failed' AND e.at >= $1 AND a.status <> 'deleted'
		GROUP BY a.account_id, a.slug HAVING count(*) >= $2
		ORDER BY count(*) DESC LIMIT 1000`,
			[]any{now.Add(-api.AttentionOOMWindow), api.AttentionOOMKills}},
		{api.AttentionKindBootFailing, `
		SELECT a.account_id::text, $3::text, a.slug,
		       left(coalesce((array_agg(e.data->>'reason' ORDER BY e.at DESC))[1], ''), 300), count(*)
		FROM events e JOIN apps a ON a.id::text = e.data->>'app_id'
		WHERE e.kind = 'wake.boot_failed' AND e.at >= $1 AND a.status <> 'deleted'
		GROUP BY a.account_id, a.slug HAVING count(*) >= $2
		ORDER BY count(*) DESC LIMIT 1000`,
			[]any{now.Add(-api.AttentionBootWindow), api.AttentionBootFailures}},
		// The newest finished scheduled runs of each active job; a cancelled
		// run breaks the streak.
		{api.AttentionKindJobsFailing, `
		WITH recent AS (
			SELECT r.job_id, r.aggregate_status,
			       row_number() OVER (PARTITION BY r.job_id ORDER BY r.finished_at DESC, r.id DESC) AS rn
			FROM job_runs r
			WHERE r.trigger_kind = 'scheduled' AND r.finished_at >= $1
			  AND r.aggregate_status IN ('succeeded', 'failed', 'dead_letter', 'cancelled'))
		SELECT j.account_id::text, $3::text, j.name,
		       'last ' || count(*) || ' scheduled runs failed (' || string_agg(DISTINCT x.aggregate_status, ', ' ORDER BY x.aggregate_status) || ')', count(*)
		FROM recent x JOIN jobs j ON j.id = x.job_id
		WHERE x.rn <= $2 AND j.status = 'active'
		GROUP BY j.account_id, j.id, j.name
		HAVING count(*) = $2 AND bool_and(x.aggregate_status IN ('failed', 'dead_letter'))
		LIMIT 1000`,
			[]any{now.Add(-api.AttentionJobWindow), api.AttentionJobFailures}},
		{api.AttentionKindPaymentFailing, `
		SELECT f.account_id::text, $2::text, 'billing',
		       'payment failed (' || (array_agg(f.event_type ORDER BY f.processed_at DESC))[1] || ') and has not recovered', count(*)
		FROM billing_payment_failure_receipts f JOIN accounts a ON a.id = f.account_id
		WHERE f.processed_at >= $1
		GROUP BY f.account_id
		HAVING NOT EXISTS (SELECT 1 FROM billing_payment_recovery_receipts r
		                   WHERE r.account_id = f.account_id AND r.processed_at > max(f.processed_at))
		LIMIT 1000`,
			[]any{now.Add(-api.AttentionPaymentWindow)}},
		{api.AttentionKindLimitPressure, `
		SELECT account_id::text, $3::text, code,
		       count(*) || ' refusals on ' || count(DISTINCT (occurred_at AT TIME ZONE 'UTC')::date) || ' days',
		       count(DISTINCT (occurred_at AT TIME ZONE 'UTC')::date)
		FROM customer_api_errors
		WHERE occurred_at >= $1 AND ` + limitCodePredicate + `
		GROUP BY account_id, code HAVING count(DISTINCT (occurred_at AT TIME ZONE 'UTC')::date) >= $2
		ORDER BY count(*) DESC LIMIT 1000`,
			[]any{now.Add(-api.AttentionLimitWindow), api.AttentionLimitDays}},
	}
}

// detect runs every rule.
func detect(ctx context.Context, tx pgx.Tx, now time.Time) ([]Finding, error) {
	var out []Finding
	for _, r := range rules(now) {
		args := append(append([]any{}, r.args...), r.kind)
		if r.kind == api.AttentionKindRefusedBurst {
			args = append(args, refusedBurstIgnored)
		}
		found, err := collect(ctx, tx, r.query, args)
		if err != nil {
			return nil, fmt.Errorf("attention: %s: %w", r.kind, err)
		}
		out = append(out, found...)
	}
	return out, nil
}

func collect(ctx context.Context, tx pgx.Tx, query string, args []any) ([]Finding, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.AccountID, &f.Kind, &f.Subject, &f.Detail, &f.Observed); err != nil {
			return nil, err
		}
		f.Detail = bound(f.Detail, 500)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Filter narrows List. Empty fields match everything; States defaults to
// open and dismissed.
type Filter struct {
	AccountID, Kind string
	States          []string
	Limit           int
}

const signalColumns = `id, account_id::text, kind, subject, detail, state, observed,
	first_seen_at, last_seen_at, resolved_at, dismissed_at, coalesce(dismissed_by::text, ''), dismiss_reason,
	coalesce(outcome, ''), coalesce(ticket_id::text, '')`

func scanSignal(row pgx.Row) (api.ObsAttentionSignal, error) {
	var s api.ObsAttentionSignal
	err := row.Scan(&s.ID, &s.AccountID, &s.Kind, &s.Subject, &s.Detail, &s.State, &s.Observed,
		&s.FirstSeenAt, &s.LastSeenAt, &s.ResolvedAt, &s.DismissedAt, &s.DismissedBy, &s.DismissReason, &s.Outcome, &s.TicketID)
	s.FirstSeenAt, s.LastSeenAt = s.FirstSeenAt.UTC(), s.LastSeenAt.UTC()
	return s, err
}

// List returns signals newest first.
func (e *Evaluator) List(ctx context.Context, f Filter) ([]api.ObsAttentionSignal, error) {
	out := []api.ObsAttentionSignal{}
	if e == nil {
		return out, nil
	}
	if len(f.States) == 0 {
		f.States = []string{api.AttentionStateOpen, api.AttentionStateDismissed}
	}
	if f.Limit <= 0 || f.Limit > api.AttentionListMax {
		f.Limit = api.AttentionListMax
	}
	rows, err := e.Pool.Query(ctx, `SELECT `+signalColumns+`
		FROM customer_attention_signals
		WHERE state = ANY($1) AND ($2 = '' OR account_id::text = $2) AND ($3 = '' OR kind = $3)
		ORDER BY last_seen_at DESC, id DESC LIMIT $4`, f.States, f.AccountID, f.Kind, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("attention: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSignal(rows)
		if err != nil {
			return nil, fmt.Errorf("attention: list: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns one signal, or ErrNotFound.
func (e *Evaluator) Get(ctx context.Context, id int64) (api.ObsAttentionSignal, error) {
	if e == nil {
		return api.ObsAttentionSignal{}, ErrNotFound
	}
	s, err := scanSignal(e.Pool.QueryRow(ctx, `SELECT `+signalColumns+` FROM customer_attention_signals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, fmt.Errorf("attention: get: %w", err)
	}
	return s, nil
}

// Dismiss marks an open signal dismissed by staffID with outcome (one of
// api.AttentionDismissOutcomes) and an optional note.
func (e *Evaluator) Dismiss(ctx context.Context, id int64, staffID, outcome, note string) error {
	if e == nil {
		return errors.New("attention: unavailable")
	}
	if !slices.Contains(api.AttentionDismissOutcomes(), outcome) {
		return fmt.Errorf("%w: outcome must be one of %s", ErrInvalid, strings.Join(api.AttentionDismissOutcomes(), ", "))
	}
	note = strings.TrimSpace(note)
	if len(note) > api.AttentionDismissReasonMax {
		return fmt.Errorf("%w: note must be at most %d bytes", ErrInvalid, api.AttentionDismissReasonMax)
	}
	tag, err := e.Pool.Exec(ctx, `UPDATE customer_attention_signals
		SET state = 'dismissed', dismissed_at = $2, dismissed_by = $3::uuid, outcome = $4, dismiss_reason = $5
		WHERE id = $1 AND state = 'open'`, id, e.Now().UTC(), staffID, outcome, note)
	if err != nil {
		return fmt.Errorf("attention: dismiss: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Contacted records that staffID contacted the customer about an open
// signal through ticketID: the signal is dismissed with outcome contacted.
// Repeating it with the same ticket is a no-op.
func (e *Evaluator) Contacted(ctx context.Context, id int64, staffID, ticketID string) error {
	if e == nil {
		return errors.New("attention: unavailable")
	}
	tag, err := e.Pool.Exec(ctx, `UPDATE customer_attention_signals
		SET state = 'dismissed', dismissed_at = coalesce(dismissed_at, $2), dismissed_by = $3::uuid,
		    outcome = 'contacted', ticket_id = $4::uuid
		WHERE id = $1 AND (state = 'open' OR (state = 'dismissed' AND outcome = 'contacted' AND ticket_id = $4::uuid))`,
		id, e.Now().UTC(), staffID, ticketID)
	if err != nil {
		return fmt.Errorf("attention: contacted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// KindSummary is how one kind of signal fared over a period: how many
// opened, what support did, and how long it took to act.
type KindSummary struct {
	Kind                                  string
	Total, Open, Contacted, FalsePositive int64
	OtherDismissed, ResolvedOnOwn         int64
	// MedianActSeconds is the median time from first seen to contact or
	// dismissal; nil when nobody acted.
	MedianActSeconds *float64
}

// FalsePositivePercent is the share of acted-on signals support marked
// false_positive, or -1 when none were acted on.
func (k KindSummary) FalsePositivePercent() int64 {
	acted := k.Contacted + k.FalsePositive + k.OtherDismissed
	if acted == 0 {
		return -1
	}
	return k.FalsePositive * 100 / acted
}

// MedianAct renders MedianActSeconds for people ("" when nobody acted).
func (k KindSummary) MedianAct() string {
	if k.MedianActSeconds == nil {
		return ""
	}
	return time.Duration(*k.MedianActSeconds * float64(time.Second)).Round(time.Minute).String()
}

// Summary summarises signals first seen since since, one row per kind that
// has any, in api.AttentionKinds order.
func (e *Evaluator) Summary(ctx context.Context, since time.Time) ([]KindSummary, error) {
	out := []KindSummary{}
	if e == nil {
		return out, nil
	}
	rows, err := e.Pool.Query(ctx, `SELECT kind, count(*),
		count(*) FILTER (WHERE state = 'open'),
		count(*) FILTER (WHERE outcome = 'contacted'),
		count(*) FILTER (WHERE outcome = 'false_positive'),
		count(*) FILTER (WHERE outcome IN ('platform_issue', 'already_in_touch', 'not_needed')),
		count(*) FILTER (WHERE outcome = 'resolved_on_own'),
		percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM dismissed_at - first_seen_at))
			FILTER (WHERE dismissed_at IS NOT NULL AND outcome IS NOT NULL AND outcome <> 'resolved_on_own')
		FROM customer_attention_signals WHERE first_seen_at >= $1 GROUP BY kind`, since)
	if err != nil {
		return nil, fmt.Errorf("attention: summary: %w", err)
	}
	defer rows.Close()
	byKind := map[string]KindSummary{}
	for rows.Next() {
		var k KindSummary
		if err := rows.Scan(&k.Kind, &k.Total, &k.Open, &k.Contacted, &k.FalsePositive, &k.OtherDismissed, &k.ResolvedOnOwn, &k.MedianActSeconds); err != nil {
			return nil, fmt.Errorf("attention: summary: %w", err)
		}
		byKind[k.Kind] = k
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attention: summary: %w", err)
	}
	for _, kind := range api.AttentionKinds() {
		if k, ok := byKind[kind]; ok {
			out = append(out, k)
		}
	}
	return out, nil
}

func bound(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}
