// Package attention finds customers who are probably stuck and keeps one
// signal per account, kind and subject for support (ADR-20261113123000).
//
// An evaluation runs the three rules against data apid already stores
// (failed deployments, recorded API refusals, custom-domain status), opens
// or refreshes a signal for every finding, and resolves every active signal
// the rules no longer find. Support may dismiss an open signal; a dismissed
// signal stays quiet until its condition clears, after which a recurrence
// opens a fresh one. Signals hold identifiers only.
package attention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// evaluateLockKey serializes evaluations across apid replicas
// (pg_try_advisory_xact_lock).
const evaluateLockKey int64 = 0x617474656e74696f // "attentio"

// ErrNotFound is returned by Dismiss when no open signal has the id.
var ErrNotFound = errors.New("attention: no open signal with this id")

// refusedBurstIgnored are problem codes that repeat in healthy use.
var refusedBurstIgnored = []string{api.CodeNotFound}

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
	Skipped                             bool
}

// Evaluator evaluates the rules and serves signal reads and dismissals. A
// nil *Evaluator reads nothing and evaluates nothing (apid without
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
	result := Result{OpenByKind: map[string]int64{}}
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
	tag, err := tx.Exec(ctx, `UPDATE customer_attention_signals SET state = 'resolved', resolved_at = $1
		WHERE state IN ('open','dismissed') AND last_seen_at < $1`, now)
	if err != nil {
		return Result{}, fmt.Errorf("attention: resolve: %w", err)
	}
	result.Resolved = tag.RowsAffected()
	tag, err = tx.Exec(ctx, `DELETE FROM customer_attention_signals WHERE id IN (
		SELECT id FROM customer_attention_signals WHERE state = 'resolved' AND resolved_at < $1 ORDER BY resolved_at LIMIT 5000)`,
		now.Add(-api.AttentionResolvedRetention))
	if err != nil {
		return Result{}, fmt.Errorf("attention: prune: %w", err)
	}
	result.Pruned = tag.RowsAffected()
	rows, err := tx.Query(ctx, `SELECT kind, count(*) FROM customer_attention_signals WHERE state = 'open' GROUP BY kind`)
	if err != nil {
		return Result{}, fmt.Errorf("attention: count: %w", err)
	}
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return Result{}, err
		}
		result.OpenByKind[kind] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("attention: commit: %w", err)
	}
	if e.Observe != nil {
		e.Observe(result)
	}
	return result, nil
}

// detect runs the three rules.
func detect(ctx context.Context, tx pgx.Tx, now time.Time) ([]Finding, error) {
	var out []Finding
	collect := func(name, query string, args ...any) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("attention: %s: %w", name, err)
		}
		defer rows.Close()
		for rows.Next() {
			var f Finding
			if err := rows.Scan(&f.AccountID, &f.Kind, &f.Subject, &f.Detail, &f.Observed); err != nil {
				return fmt.Errorf("attention: %s: %w", name, err)
			}
			f.Detail = bound(f.Detail, 500)
			out = append(out, f)
		}
		return rows.Err()
	}
	if err := collect("deploy_failing", `
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
		now.Add(-api.AttentionDeployWindow), api.AttentionDeployFailures, api.AttentionKindDeployFailing); err != nil {
		return nil, err
	}
	if err := collect("refused_burst", `
		SELECT account_id::text, $3::text, code, '', count(*)
		FROM customer_api_errors
		WHERE occurred_at >= $1 AND NOT (code = ANY($4))
		GROUP BY account_id, code HAVING count(*) >= $2
		ORDER BY count(*) DESC LIMIT 1000`,
		now.Add(-api.AttentionRefusedWindow), api.AttentionRefusedBurst, api.AttentionKindRefusedBurst, refusedBurstIgnored); err != nil {
		return nil, err
	}
	if err := collect("domain_stuck", `
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
		now.Add(-api.AttentionDomainFailedAfter), now.Add(-api.AttentionDomainUnverifiedAfter), api.AttentionKindDomainStuck); err != nil {
		return nil, err
	}
	return out, nil
}

// Filter narrows List. Empty fields match everything; States defaults to
// open and dismissed.
type Filter struct {
	AccountID, Kind string
	States          []string
	Limit           int
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
	rows, err := e.Pool.Query(ctx, `SELECT id, account_id::text, kind, subject, detail, state, observed,
		first_seen_at, last_seen_at, resolved_at, dismissed_at, coalesce(dismissed_by::text, ''), dismiss_reason
		FROM customer_attention_signals
		WHERE state = ANY($1) AND ($2 = '' OR account_id::text = $2) AND ($3 = '' OR kind = $3)
		ORDER BY last_seen_at DESC, id DESC LIMIT $4`, f.States, f.AccountID, f.Kind, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("attention: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s api.ObsAttentionSignal
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Kind, &s.Subject, &s.Detail, &s.State, &s.Observed,
			&s.FirstSeenAt, &s.LastSeenAt, &s.ResolvedAt, &s.DismissedAt, &s.DismissedBy, &s.DismissReason); err != nil {
			return nil, fmt.Errorf("attention: list: %w", err)
		}
		s.FirstSeenAt, s.LastSeenAt = s.FirstSeenAt.UTC(), s.LastSeenAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// Dismiss marks an open signal dismissed by staffID with reason.
func (e *Evaluator) Dismiss(ctx context.Context, id int64, staffID, reason string) error {
	if e == nil {
		return errors.New("attention: unavailable")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > api.AttentionDismissReasonMax {
		return fmt.Errorf("attention: dismiss reason must be 1 to %d bytes", api.AttentionDismissReasonMax)
	}
	tag, err := e.Pool.Exec(ctx, `UPDATE customer_attention_signals
		SET state = 'dismissed', dismissed_at = $2, dismissed_by = $3::uuid, dismiss_reason = $4
		WHERE id = $1 AND state = 'open'`, id, e.Now().UTC(), staffID, reason)
	if err != nil {
		return fmt.Errorf("attention: dismiss: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
