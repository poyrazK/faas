// PgStore implementations for issue #476 (ADR-076) — outbound
// webhook subscriptions + delivery ledger. Raw-SQL implementations
// (not sqlc) so the dispatcher can ship without a sqlc regen. The
// next sqlc regen (issue #476 follow-up or any PR touching
// pkg/state/sqlc/) will pick these up automatically. Mirrors the
// CreateCronIfUnderQuota pattern (pgstore.go:4191-4270) for the
// quota gate and the cron dispatcher pattern for the claim
// transaction.
//
// The dispatcher's claim transaction (ClaimDueAppWebhookDeliveries)
// is the hot path: it must be a single round-trip + single
// FOR UPDATE SKIP LOCKED claim so two schedd instances cannot
// process the same row. Mirrors pkg/sched/drain.go's "FOR UPDATE
// SKIP LOCKED" claim shape (cron dispatcher).
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// CreateAppWebhook is the un-capped insert path used by tests.
// Production callers use CreateAppWebhookIfUnderQuota.
func (s *PgStore) CreateAppWebhook(ctx context.Context, in AppWebhook) (AppWebhook, error) {
	filterArr := in.EventFilter
	if filterArr == nil {
		filterArr = []string{}
	}
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	row := s.pool.QueryRow(ctx, `
		insert into app_webhooks
			(app_id, account_id, target_url, secret_sealed,
			 event_filter, retry_policy, delivery_format, enabled)
		values ($1, $2, $3, $4, $5::text[], $6, $7, $8)
		returning id, app_id, account_id, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled,
		          created_at, updated_at
	`, in.AppID, in.AccountID, in.TargetURL, in.SecretSealed,
		filterArr, string(in.RetryPolicy), string(in.DeliveryFormat), in.Enabled)
	w, err := scanAppWebhook(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppWebhook{}, ErrConflict
		}
		return AppWebhook{}, fmt.Errorf("state: insert app_webhook: %w", err)
	}
	return w, nil
}

// CreateAppWebhookIfUnderQuota mirrors CreateCronIfUnderQuota:
// locks the parent apps row, counts per-app + per-account under
// the same transaction, inserts under the lock. Returns
// AppWebhookQuotaError on cap trips, ErrNotFound when the app row
// is missing, ErrConflict on a duplicate (app_id, target_url).
func (s *PgStore) CreateAppWebhookIfUnderQuota(ctx context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppWebhook{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var locked int
	err = tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status <> 'deleted' for update`,
		in.AppID,
	).Scan(&locked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: lock app %s: %w", in.AppID, err)
	}

	var appCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from app_webhooks where app_id = $1`, in.AppID,
	).Scan(&appCount); err != nil {
		return AppWebhook{}, fmt.Errorf("state: count app_webhooks for app %s: %w", in.AppID, err)
	}
	if appCount >= limits.WebhookPerApp {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope:    AppWebhookQuotaScopeApp,
			Limit:    limits.WebhookPerApp,
			Observed: appCount,
		}
	}

	var accountID string
	if err := tx.QueryRow(ctx,
		`select account_id from apps where id = $1`, in.AppID,
	).Scan(&accountID); err != nil {
		return AppWebhook{}, fmt.Errorf("state: read account_id for app %s: %w", in.AppID, err)
	}
	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from app_webhooks w
		 join apps a on a.id = w.app_id
		 where a.account_id = $1 and a.status <> 'deleted'
	`, accountID).Scan(&accountCount); err != nil {
		return AppWebhook{}, fmt.Errorf("state: count app_webhooks for account %s: %w", accountID, err)
	}
	if accountCount >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope:    AppWebhookQuotaScopeAccount,
			Limit:    limits.WebhookPerAccount,
			Observed: accountCount,
		}
	}

	filterArr := in.EventFilter
	if filterArr == nil {
		filterArr = []string{}
	}
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	row := tx.QueryRow(ctx, `
		insert into app_webhooks
			(app_id, account_id, target_url, secret_sealed,
			 event_filter, retry_policy, delivery_format, enabled)
		values ($1, $2, $3, $4, $5::text[], $6, $7, $8)
		returning id, app_id, account_id, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled,
		          created_at, updated_at
	`, in.AppID, in.AccountID, in.TargetURL, in.SecretSealed,
		filterArr, string(in.RetryPolicy), string(in.DeliveryFormat), in.Enabled)
	w, err := scanAppWebhook(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppWebhook{}, ErrConflict
		}
		return AppWebhook{}, fmt.Errorf("state: insert app_webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppWebhook{}, fmt.Errorf("state: commit create app_webhook: %w", err)
	}
	return w, nil
}

func (s *PgStore) AppWebhookByID(ctx context.Context, id string) (AppWebhook, error) {
	row := s.pool.QueryRow(ctx, `
		select id, app_id, account_id, target_url, secret_sealed,
		       event_filter, retry_policy, delivery_format, enabled,
		       created_at, updated_at
		  from app_webhooks where id = $1
	`, id)
	w, err := scanAppWebhook(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: read app_webhook: %w", err)
	}
	return w, nil
}

// UpdateAppWebhook mirrors UpdateAlertRule's nil-skip semantics.
// Only the supplied fields are touched; nil pointer fields stay
// unchanged.
func (s *PgStore) UpdateAppWebhook(ctx context.Context, id string, p UpdateAppWebhookParams) (AppWebhook, error) {
	// Build a sparse UPDATE so the existing row's values stay intact
	// for any column the caller didn't pass.
	current, err := s.AppWebhookByID(ctx, id)
	if err != nil {
		return AppWebhook{}, err
	}
	if p.TargetURL != nil {
		current.TargetURL = *p.TargetURL
	}
	if p.EventFilter != nil {
		current.EventFilter = append([]string(nil), *p.EventFilter...)
	}
	if p.RetryPolicy != nil {
		current.RetryPolicy = *p.RetryPolicy
	}
	if p.DeliveryFormat != nil {
		current.DeliveryFormat = *p.DeliveryFormat
	}
	if p.Enabled != nil {
		current.Enabled = *p.Enabled
	}
	if p.WebhookSecretSealed != nil {
		current.SecretSealed = append([]byte(nil), *p.WebhookSecretSealed...)
	}
	filterArr := current.EventFilter
	if filterArr == nil {
		filterArr = []string{}
	}
	if current.DeliveryFormat == "" {
		current.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	row := s.pool.QueryRow(ctx, `
		update app_webhooks set
			target_url = $2,
			event_filter = $3::text[],
			retry_policy = $4,
			delivery_format = $5,
			enabled = $6,
			secret_sealed = $7,
			updated_at = now()
		where id = $1
		returning id, app_id, account_id, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled,
		          created_at, updated_at
	`, id, current.TargetURL, filterArr, string(current.RetryPolicy),
		string(current.DeliveryFormat), current.Enabled, current.SecretSealed)
	w, err := scanAppWebhook(row)
	if err != nil {
		return AppWebhook{}, fmt.Errorf("state: update app_webhook: %w", err)
	}
	return w, nil
}

func (s *PgStore) DeleteAppWebhook(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from app_webhooks where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete app_webhook: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAppWebhooksForApp(ctx context.Context, appID string) ([]AppWebhook, error) {
	rows, err := s.pool.Query(ctx, `
		select id, app_id, account_id, target_url, secret_sealed,
		       event_filter, retry_policy, delivery_format, enabled,
		       created_at, updated_at
		  from app_webhooks
		 where app_id = $1
		 order by created_at desc
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list app_webhooks for app: %w", err)
	}
	defer rows.Close()
	return scanAppWebhooks(rows)
}

func (s *PgStore) ListAppWebhooksForAccount(ctx context.Context, accountID string) ([]AppWebhook, error) {
	rows, err := s.pool.Query(ctx, `
		select id, app_id, account_id, target_url, secret_sealed,
		       event_filter, retry_policy, delivery_format, enabled,
		       created_at, updated_at
		  from app_webhooks
		 where account_id = $1
		 order by created_at desc
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list app_webhooks for account: %w", err)
	}
	defer rows.Close()
	return scanAppWebhooks(rows)
}

// RecordAppWebhookDelivery is the apid-side enqueue. The
// dispatcher's claim query picks the row up at next_attempt_at <=
// now().
func (s *PgStore) RecordAppWebhookDelivery(ctx context.Context, in AppWebhookDelivery) (AppWebhookDelivery, error) {
	if in.Status == "" {
		in.Status = AppWebhookDeliveryPending
	}
	if in.NextAttemptAt.IsZero() {
		in.NextAttemptAt = time.Now()
	}
	row := s.pool.QueryRow(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload,
			 attempt, status, next_attempt_at)
		values ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
		returning id, webhook_id, app_id, account_id, event, payload,
		          attempt, status, last_error, last_response_code,
		          next_attempt_at, delivered_at, created_at, updated_at
	`, in.WebhookID, in.AppID, in.AccountID, string(in.Event),
		string(in.Payload), in.Attempt, string(in.Status), in.NextAttemptAt)
	d, err := scanAppWebhookDelivery(row)
	if err != nil {
		return AppWebhookDelivery{}, fmt.Errorf("state: insert app_webhook_delivery: %w", err)
	}
	return d, nil
}

// ClaimDueAppWebhookDeliveries is the dispatcher's tick entry. In
// a single transaction it:
//  1. Locks up to `limit` rows with FOR UPDATE SKIP LOCKED so two
//     schedd instances cannot process the same row.
//  2. Transitions status='pending' OR 'in_flight' (orphaned by a
//     dispatcher restart) → 'in_flight'.
//  3. Returns the locked rows.
//
// Mirrors pkg/sched/drain.go's claim shape. The ORDER BY
// account_id, next_attempt_at produces per-account round-robin:
// within a single 32/tick batch, account A's first row precedes
// account B's first row, which precedes account C's first row,
// etc. Combined with the 32/tick cap, no account gets more than
// ceil(32/N) rows in a single tick for an N-account fleet.
func (s *PgStore) ClaimDueAppWebhookDeliveries(ctx context.Context, limit int, now time.Time) ([]AppWebhookDelivery, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	rows, err := tx.Query(ctx, `
		select id, webhook_id, app_id, account_id, event, payload,
		       attempt, status, last_error, last_response_code,
		       next_attempt_at, delivered_at, created_at, updated_at
		  from app_webhook_deliveries
		 where status in ('pending','in_flight')
		   and next_attempt_at <= $1
		 order by account_id, next_attempt_at
		 limit $2
		   for update skip locked
	`, now, limit)
	// Index app_webhook_deliveries_pending_idx
	// (account_id, next_attempt_at) WHERE status IN ('pending','in_flight')
	// covers both the bounding predicate and the round-robin ORDER BY.
	if err != nil {
		return nil, fmt.Errorf("state: claim query: %w", err)
	}
	var claimed []AppWebhookDelivery
	for rows.Next() {
		d, err := scanAppWebhookDeliveryInto(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: scan claim row: %w", err)
		}
		claimed = append(claimed, d)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, fmt.Errorf("state: claim rows: %w", rows.Err())
	}

	if len(claimed) == 0 {
		// Nothing to do — commit the empty tx so we don't hold a
		// read lock on the partial index.
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("state: commit empty claim: %w", err)
		}
		return nil, nil
	}

	// Transition pending → in_flight for the claimed rows. The
	// 'in_flight' rows that re-appear in the claim (orphaned by a
	// dispatcher restart) stay 'in_flight' — the mark methods below
	// move them to succeeded / failed / dead at attempt end.
	ids := make([]string, len(claimed))
	for i, d := range claimed {
		ids[i] = d.ID
		d.Status = AppWebhookDeliveryInFlight
		d.UpdatedAt = now
		claimed[i] = d
	}
	if _, err := tx.Exec(ctx, `
		update app_webhook_deliveries
		   set status = 'in_flight', updated_at = $2
		 where id = any($1::uuid[])
	`, ids, now); err != nil {
		return nil, fmt.Errorf("state: mark in_flight: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit claim: %w", err)
	}
	return claimed, nil
}

func (s *PgStore) MarkAppWebhookDeliverySucceeded(ctx context.Context, id string, responseCode, currentAttempt int, deliveredAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		update app_webhook_deliveries set
			status = 'succeeded',
			delivered_at = $2,
			last_response_code = $3,
			attempt = $4,
			next_attempt_at = 'epoch'::timestamptz,
			updated_at = now()
		where id = $1
	`, id, deliveredAt, responseCode, currentAttempt+1)
	if err != nil {
		return fmt.Errorf("state: mark succeeded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) MarkAppWebhookDeliveryFailed(ctx context.Context, id string, responseCode, currentAttempt int, errMsg string, nextAttemptAt time.Time) error {
	// Reset status='pending' (not 'failed') so the dispatcher's
	// partial-index claim (`WHERE status IN ('pending','in_flight')
	// AND next_attempt_at <= now()`) re-picks the row up when the
	// rescheduled time arrives. last_error + last_response_code
	// preserve the historical failure record.
	tag, err := s.pool.Exec(ctx, `
		update app_webhook_deliveries set
			status = 'pending',
			next_attempt_at = $2,
			last_response_code = $3,
			last_error = $4,
			attempt = $5,
			updated_at = now()
		where id = $1
	`, id, nextAttemptAt, responseCode, errMsg, currentAttempt+1)
	if err != nil {
		return fmt.Errorf("state: mark failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) MarkAppWebhookDeliveryDead(ctx context.Context, id string, currentAttempt int, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `
		update app_webhook_deliveries set
			status = 'dead',
			last_error = $2,
			attempt = $3,
			next_attempt_at = 'epoch'::timestamptz,
			updated_at = now()
		where id = $1
	`, id, errMsg, currentAttempt+1)
	if err != nil {
		return fmt.Errorf("state: mark dead: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ResetAppWebhookDeliveryFromDead(ctx context.Context, id, webhookID, accountID string, now time.Time) error {
	// IDOR-safe: only return success when the row matches
	// (id, webhook_id, account_id, status='dead'). A row whose
	// account_id or webhook_id differs from the caller's is
	// indistinguishable from a missing row to the caller —
	// we don't leak existence of foreign rows. The probe
	// below only distinguishes "wrong status" (ErrConflict,
	// since the caller must be the owner — they had to look
	// it up to find the id) from "no such row" (ErrNotFound).
	tag, err := s.pool.Exec(ctx, `
		update app_webhook_deliveries set
			status = 'pending',
			attempt = 0,
			last_error = '',
			next_attempt_at = $4,
			updated_at = now()
		where id = $1 and webhook_id = $2 and account_id = $3 and status = 'dead'
	`, id, webhookID, accountID, now)
	if err != nil {
		return fmt.Errorf("state: reset from dead: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Probe ownership first. If the row exists AND its
		// (webhook_id, account_id) match the caller but status
		// is not 'dead' → ErrConflict (caller owns it; wrong
		// state). If ownership mismatches OR the row doesn't
		// exist → ErrNotFound (no leak).
		var ownedByCaller bool
		if err := s.pool.QueryRow(ctx,
			`select exists(select 1 from app_webhook_deliveries
			   where id = $1 and webhook_id = $2 and account_id = $3)`,
			id, webhookID, accountID,
		).Scan(&ownedByCaller); err != nil {
			return fmt.Errorf("state: probe delivery: %w", err)
		}
		if !ownedByCaller {
			return ErrNotFound
		}
		return ErrConflict
	}
	return nil
}

// ListAppWebhookDeliveries backs the GET deliveries endpoint.
// pageToken is the created_at RFC3339Nano + ID of the last row from
// the previous page ("" = first page). Result is ordered by
// created_at DESC, id DESC for stable pagination.
func (s *PgStore) ListAppWebhookDeliveries(ctx context.Context, appID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	var rows pgx.Rows
	var err error
	if pageToken == "" {
		rows, err = s.pool.Query(ctx, `
			select id, webhook_id, app_id, account_id, event, payload,
			       attempt, status, last_error, last_response_code,
			       next_attempt_at, delivered_at, created_at, updated_at
			  from app_webhook_deliveries
			 where app_id = $1 and webhook_id = $2
			 order by created_at desc, id desc
			 limit $3
		`, appID, webhookID, pageSize+1)
	} else {
		// pageToken shape: "<created_at_unix_nano>:<id>" — produced
		// by the previous call's nextToken. Avoids leaking
		// server-side ID sequence values.
		ts, id, ok := decodePageToken(pageToken)
		if !ok {
			return nil, "", fmt.Errorf("state: invalid page token")
		}
		rows, err = s.pool.Query(ctx, `
			select id, webhook_id, app_id, account_id, event, payload,
			       attempt, status, last_error, last_response_code,
			       next_attempt_at, delivered_at, created_at, updated_at
			  from app_webhook_deliveries
			 where app_id = $1 and webhook_id = $2
			   and (created_at, id) < ($3, $4)
			 order by created_at desc, id desc
			 limit $5
		`, appID, webhookID, ts, id, pageSize+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("state: list deliveries: %w", err)
	}
	defer rows.Close()
	out, err := scanAppWebhookDeliveries(rows)
	if err != nil {
		return nil, "", fmt.Errorf("state: scan deliveries: %w", err)
	}
	var nextToken string
	if pageSize > 0 && len(out) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (s *PgStore) AppWebhookDeliveryByID(ctx context.Context, id string) (AppWebhookDelivery, error) {
	row := s.pool.QueryRow(ctx, `
		select id, webhook_id, app_id, account_id, event, payload,
		       attempt, status, last_error, last_response_code,
		       next_attempt_at, delivered_at, created_at, updated_at
		  from app_webhook_deliveries where id = $1
	`, id)
	d, err := scanAppWebhookDelivery(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhookDelivery{}, ErrNotFound
		}
		return AppWebhookDelivery{}, fmt.Errorf("state: read delivery: %w", err)
	}
	return d, nil
}

// ----------------------------------------------------------------------------
// scanner helpers
// ----------------------------------------------------------------------------

// appWebhookScanner is the minimal Scan(dest ...any) error
// interface both pgx.Row and pgx.Rows satisfy. Centralising the
// field list here means a future row layout change touches one
// place.
type appWebhookScanner interface {
	Scan(dest ...any) error
}

func scanAppWebhook(s appWebhookScanner) (AppWebhook, error) {
	var (
		w      AppWebhook
		filter []string
		retry  string
		format string
	)
	err := s.Scan(
		&w.ID, &w.AppID, &w.AccountID, &w.TargetURL, &w.SecretSealed,
		&filter, &retry, &format, &w.Enabled, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return AppWebhook{}, err
	}
	w.RetryPolicy = AppWebhookRetryPolicy(retry)
	w.DeliveryFormat = AppWebhookDeliveryFormat(format)
	if w.DeliveryFormat == "" {
		w.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	w.EventFilter = filter
	if w.EventFilter == nil {
		w.EventFilter = []string{}
	}
	return w, nil
}

func scanAppWebhooks(rows pgx.Rows) ([]AppWebhook, error) {
	var out []AppWebhook
	for rows.Next() {
		w, err := scanAppWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan app_webhook: %w", err)
		}
		out = append(out, w)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("state: rows: %w", rows.Err())
	}
	return out, nil
}

func scanAppWebhookDelivery(s appWebhookScanner) (AppWebhookDelivery, error) {
	var (
		d          AppWebhookDelivery
		payload    []byte
		status     string
		event      string
		lastErr    *string
		lastRespCo *int32
	)
	err := s.Scan(
		&d.ID, &d.WebhookID, &d.AppID, &d.AccountID, &event, &payload,
		&d.Attempt, &status, &lastErr, &lastRespCo,
		&d.NextAttemptAt, &d.DeliveredAt, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return AppWebhookDelivery{}, err
	}
	d.Event = AppWebhookEvent(event)
	d.Status = AppWebhookDeliveryStatus(status)
	d.Payload = json.RawMessage(payload)
	if lastErr != nil {
		d.LastError = *lastErr
	}
	if lastRespCo != nil {
		d.LastResponseCode = int(*lastRespCo)
	}
	return d, nil
}

func scanAppWebhookDeliveryInto(s appWebhookScanner) (AppWebhookDelivery, error) {
	return scanAppWebhookDelivery(s)
}

func scanAppWebhookDeliveries(rows pgx.Rows) ([]AppWebhookDelivery, error) {
	var out []AppWebhookDelivery
	for rows.Next() {
		d, err := scanAppWebhookDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan delivery: %w", err)
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("state: rows: %w", rows.Err())
	}
	return out, nil
}

// isUniqueViolation checks pgconn.PgError.Code == "23505". Mirrors
// the existing helper used in pgstore.go for the same purpose.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// encodePageToken + decodePageToken round-trip "<unix_nano>:<id>".
// The unix_nano form avoids float precision drift and lets the
// pagination query reuse the (created_at, id) < ($1, $2) predicate.
func encodePageToken(t time.Time, id string) string {
	return fmt.Sprintf("%d:%s", t.UnixNano(), id)
}

func decodePageToken(token string) (time.Time, string, bool) {
	var nanos int64
	var id string
	for i := 0; i < len(token); i++ {
		if token[i] == ':' {
			_, err := fmt.Sscanf(token[:i], "%d", &nanos)
			if err != nil {
				return time.Time{}, "", false
			}
			id = token[i+1:]
			// UUID v4 validation — pgx accepts any text-shaped id
			// but a malformed UUID would error on the row-comparison
			// predicate with a noisy pgx error. Reject here so the
			// caller returns a clean 400. Accepts the 32-hex form
			// Postgres stores (no dashes) AND the dashed form in
			// case an old page-token format slips through.
			stripped := strings.ReplaceAll(id, "-", "")
			if len(stripped) != 32 {
				return time.Time{}, "", false
			}
			for _, r := range stripped {
				if (r < '0' || r > '9') && (r < 'A' || r > 'F') && (r < 'a' || r > 'f') {
					return time.Time{}, "", false
				}
			}
			return time.Unix(0, nanos).UTC(), id, true
		}
	}
	return time.Time{}, "", false
}
