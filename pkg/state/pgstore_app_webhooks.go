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
// locks subscriptions before reading their live lease counts. Its second
// statement has a fresh READ COMMITTED snapshot, so concurrent schedulers
// cannot both spend the same per-subscription capacity. Delivery rows are
// then claimed with FOR UPDATE SKIP LOCKED.
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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

// CreateAppWebhook is the un-capped insert path used by tests.
// Production callers use CreateAppWebhookIfUnderQuota.
func (s *PgStore) CreateAppWebhook(ctx context.Context, in AppWebhook) (AppWebhook, error) {
	if in.Scope != "" && in.Scope != AppWebhookScopeApp {
		return AppWebhook{}, ErrInvalidAppWebhookScope
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
	row := s.pool.QueryRow(ctx, `
		insert into app_webhooks
			(app_id, account_id, target_url, secret_sealed,
			 event_filter, retry_policy, delivery_format, enabled)
		values ($1, $2, $3, $4, $5::text[], $6, $7, $8)
		returning id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
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

// CreateAppWebhookIfUnderQuota locks the account before the app, counts
// both scopes against the shared account cap and app rows against the app
// cap, then inserts. Returns AppWebhookQuotaError on cap trips,
// ErrNotFound on a missing/foreign app, and ErrConflict on a duplicate.
func (s *PgStore) CreateAppWebhookIfUnderQuota(ctx context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	if in.Scope != "" && in.Scope != AppWebhookScopeApp {
		return AppWebhook{}, ErrInvalidAppWebhookScope
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppWebhook{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	// Use one account lock for the shared cap across different apps. Acquire
	// it before the app lock, matching app-creation's lock order.
	var locked int
	err = tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, in.AccountID).Scan(&locked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: lock webhook account %s: %w", in.AccountID, err)
	}
	err = tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and account_id = $2 and status <> 'deleted' for update`,
		in.AppID, in.AccountID,
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

	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from app_webhooks w
		 left join apps a on a.id = w.app_id
		 where w.account_id = $1
		   and (w.scope in ('account', 'platform_tenant') or
		        (w.scope = 'app' and a.account_id = $1 and a.status <> 'deleted'))
	`, in.AccountID).Scan(&accountCount); err != nil {
		return AppWebhook{}, fmt.Errorf("state: count app_webhooks for account %s: %w", in.AccountID, err)
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
		returning id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
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
		select id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
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
	if current.Scope == AppWebhookScopePlatformTenant && p.EventFilter != nil {
		return AppWebhook{}, ErrInvalidAppWebhookScope
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
			receiver_cooldown_until = case when target_url is distinct from $2 then null else receiver_cooldown_until end,
			receiver_recovery_probe_delivery_id = case when target_url is distinct from $2 then null else receiver_recovery_probe_delivery_id end,
			event_filter = $3::text[],
			retry_policy = $4,
			delivery_format = $5,
			enabled = $6,
			secret_sealed = $7,
			updated_at = now()
		where id = $1
		returning id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled,
		          created_at, updated_at
	`, id, current.TargetURL, filterArr, string(current.RetryPolicy),
		string(current.DeliveryFormat), current.Enabled, current.SecretSealed)
	w, err := scanAppWebhook(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppWebhook{}, ErrConflict
		}
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
		select id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
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
		select id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
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
		values ($1, nullif($2, '')::uuid, $3, $4, $5::jsonb, $6, $7, $8)
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
//  1. Locks eligible subscriptions with FOR UPDATE SKIP LOCKED.
//  2. Counts their live leases in a fresh snapshot and locks due delivery
//     rows with FOR UPDATE SKIP LOCKED, bounded by the subscription cap.
//  3. Transitions status='pending' OR 'in_flight' (orphaned by a
//     dispatcher restart) → 'in_flight'.
//  4. Returns the claimed rows.
//
// An active receiver cooldown excludes the subscription from new claims. Once
// it expires, only one recovery probe may be live until its outcome is known.
// Each claim batch interleaves accounts, then subscriptions within each
// account. Both starting positions rotate every five-second tick. The lateral
// delivery read is bounded per subscription, so one deep backlog cannot fill
// the account's candidate window before another subscription is considered.
func (s *PgStore) ClaimDueAppWebhookDeliveries(ctx context.Context, limit int, now time.Time) ([]AppWebhookDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	rotation := now.Unix() / appWebhookFairnessPeriodSeconds * int64(limit)
	// The subscription lock serializes capacity decisions across schedd
	// processes. Select due subscriptions per account before taking a bounded
	// set of locks, so an older backlog cannot hide another subscription.
	lockRows, err := tx.Query(ctx, `
		with live_claims as materialized (
			select webhook_id, count(*) as n
			  from app_webhook_deliveries
			 where status = 'in_flight' and next_attempt_at > $1
			 group by webhook_id
		), eligible_accounts as materialized (
			select distinct d.account_id
			  from app_webhook_deliveries d
			  join app_webhooks w on w.id = d.webhook_id
			  left join live_claims live on live.webhook_id = d.webhook_id
			 where d.status in ('pending','in_flight') and d.next_attempt_at <= $1
			   and (w.receiver_cooldown_until is null or w.receiver_cooldown_until <= $1)
			   and coalesce(live.n, 0) < case when w.receiver_cooldown_until is null then $4 else 1 end
		), numbered_accounts as (
			select account_id,
			       row_number() over (order by account_id) - 1 as account_pos,
			       count(*) over () as account_count
			  from eligible_accounts
		), selected_accounts as materialized (
			select account_id,
			       (account_pos + $3::bigint) % account_count as turn
			  from numbered_accounts
			 order by turn
			 limit ($2::integer * 2)
		), eligible_hooks as materialized (
			select w.id, w.account_id, selected_accounts.turn as account_turn
			  from selected_accounts
			  join app_webhooks w on w.account_id = selected_accounts.account_id
			  left join live_claims live on live.webhook_id = w.id
			 where (w.receiver_cooldown_until is null or w.receiver_cooldown_until <= $1)
			   and coalesce(live.n, 0) < case when w.receiver_cooldown_until is null then $4 else 1 end
			   and exists (
			       select 1 from app_webhook_deliveries d
			        where d.webhook_id = w.id
			          and d.status in ('pending','in_flight')
			          and d.next_attempt_at <= $1
			   )
		), numbered_hooks as (
			select id, account_turn,
			       row_number() over (partition by account_id order by id) - 1 as hook_pos,
			       count(*) over (partition by account_id) as hook_count
			  from eligible_hooks
		), rotated_hooks as (
			select id, account_turn,
			       (hook_pos + $3::bigint) % hook_count as hook_turn
			  from numbered_hooks
		)
		select w.id
		  from rotated_hooks h
		  join app_webhooks w on w.id = h.id
		 order by h.hook_turn, h.account_turn, w.id
		 limit ($2::integer * 2)
		   for update of w skip locked
	`, now, limit, rotation, AppWebhookMaxInFlightPerSubscription)
	if err != nil {
		return nil, fmt.Errorf("state: lock claim subscriptions: %w", err)
	}
	var webhookIDs []string
	for lockRows.Next() {
		var id string
		if err := lockRows.Scan(&id); err != nil {
			lockRows.Close()
			return nil, fmt.Errorf("state: scan claim subscription: %w", err)
		}
		webhookIDs = append(webhookIDs, id)
	}
	lockRows.Close()
	if err := lockRows.Err(); err != nil {
		return nil, fmt.Errorf("state: claim subscription rows: %w", err)
	}
	if len(webhookIDs) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("state: commit empty claim: %w", err)
		}
		return nil, nil
	}

	// This statement starts after the locks are held, so its lease count
	// sees claims committed by any prior lock holder. Each locked subscription
	// contributes bounded due rows; account and hook turns order them before
	// SKIP LOCKED and the per-subscription cap are applied.
	rows, err := tx.Query(ctx, `
		with locked_hooks as materialized (
			select w.id as webhook_id, w.account_id,
			       locked_hook.ordinality as hook_turn,
			       min(locked_hook.ordinality) over (partition by w.account_id) as account_turn
			  from unnest($3::uuid[]) with ordinality as locked_hook(webhook_id, ordinality)
			  join app_webhooks w on w.id = locked_hook.webhook_id
			 where w.receiver_cooldown_until is null or w.receiver_cooldown_until <= $1
		), due as materialized (
			select delivery.id, delivery.webhook_id, delivery.next_attempt_at,
			       locked_hooks.account_id, locked_hooks.hook_turn, locked_hooks.account_turn
			  from locked_hooks
			 cross join lateral (
				select d.id, d.webhook_id, d.next_attempt_at
				  from app_webhook_deliveries d
				 where d.webhook_id = locked_hooks.webhook_id
				   and d.status in ('pending','in_flight')
				   and d.next_attempt_at <= $1
				 order by d.next_attempt_at, d.id
				 limit ($2::integer * 4)
			 ) delivery
		), ranked_due as (
			select due.*,
			       row_number() over (
			           partition by webhook_id order by next_attempt_at, id
			       ) as hook_slot
			  from due
		), candidates as materialized (
			select ranked_due.*,
			       row_number() over (
			           partition by account_id
			           order by hook_slot, hook_turn, next_attempt_at, id
			       ) as account_slot
			  from ranked_due
		), locked as materialized (
			select d.id, d.webhook_id, d.app_id, d.account_id, d.event, d.payload,
			       d.attempt, d.status, d.last_error, d.last_response_code,
			       d.next_attempt_at, d.delivered_at, d.created_at, d.updated_at,
			       c.account_slot, c.account_turn
			  from candidates c
			  join app_webhook_deliveries d on d.id = c.id
			 where d.status in ('pending','in_flight') and d.next_attempt_at <= $1
			 order by c.account_slot, c.account_turn, c.next_attempt_at, c.id
			 limit ($2::integer * 4)
			   for update of d skip locked
		), live_claims as materialized (
			select webhook_id, count(*) as n
			  from app_webhook_deliveries
			 where webhook_id = any($3::uuid[])
			   and status = 'in_flight' and next_attempt_at > $1
			 group by webhook_id
		), ranked as (
			select locked.*,
			       row_number() over (
			           partition by webhook_id order by next_attempt_at, id
			       ) as webhook_slot
			  from locked
		)
		select r.id, r.webhook_id, r.app_id, r.account_id, r.event, r.payload,
		       r.attempt, r.status, r.last_error, r.last_response_code,
		       r.next_attempt_at, r.delivered_at, r.created_at, r.updated_at
		  from ranked r
		  left join live_claims live on live.webhook_id = r.webhook_id
		  join app_webhooks w on w.id = r.webhook_id
		 where r.webhook_slot + coalesce(live.n, 0) <= case when w.receiver_cooldown_until is null then $4 else 1 end
		 order by r.account_slot, r.account_turn, r.next_attempt_at, r.id
		 limit $2
	`, now, limit, webhookIDs, AppWebhookMaxInFlightPerSubscription)
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

	// Move the claim deadline beyond the HTTP attempt. A running claim
	// cannot be picked up again on the next five-second dispatcher tick.
	claimUntil := now.Add(AppWebhookClaimLease).UTC().Truncate(time.Microsecond)
	ids := make([]string, len(claimed))
	for i, d := range claimed {
		ids[i] = d.ID
		d.Status = AppWebhookDeliveryInFlight
		d.NextAttemptAt = claimUntil
		d.UpdatedAt = now
		claimed[i] = d
	}
	if _, err := tx.Exec(ctx, `
		update app_webhook_deliveries
		   set status = 'in_flight', next_attempt_at = $2, updated_at = $3
		 where id = any($1::uuid[])
	`, ids, claimUntil, now); err != nil {
		return nil, fmt.Errorf("state: mark in_flight: %w", err)
	}
	// These subscription rows remain locked until commit. At most one row
	// per cooling subscription survived the claim cap above.
	if _, err := tx.Exec(ctx, `
		update app_webhooks w
		   set receiver_recovery_probe_delivery_id = d.id
		  from app_webhook_deliveries d
		 where d.id = any($1::uuid[]) and d.webhook_id = w.id
		   and w.receiver_cooldown_until is not null
		   and w.receiver_cooldown_until <= $2
	`, ids, now); err != nil {
		return nil, fmt.Errorf("state: mark recovery probe: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit claim: %w", err)
	}
	return claimed, nil
}

func (s *PgStore) MarkAppWebhookDeliverySucceeded(ctx context.Context, id string, responseCode, currentAttempt int, claimUntil, deliveredAt time.Time, meta ...AppWebhookAttemptMetadata) error {
	return s.completeAppWebhookDelivery(ctx, id, currentAttempt, claimUntil, "succeeded", responseCode, "", time.Time{}, deliveredAt, meta)
}

func (s *PgStore) MarkAppWebhookDeliveryFailed(ctx context.Context, id string, responseCode, currentAttempt int, claimUntil time.Time, errMsg string, nextAttemptAt time.Time, meta ...AppWebhookAttemptMetadata) error {
	return s.completeAppWebhookDelivery(ctx, id, currentAttempt, claimUntil, "retrying", responseCode, errMsg, nextAttemptAt, time.Time{}, meta)
}

func (s *PgStore) MarkAppWebhookDeliveryDead(ctx context.Context, id string, currentAttempt int, claimUntil time.Time, errMsg string, meta ...AppWebhookAttemptMetadata) error {
	_, _, responseCode := appWebhookAttemptTimes(meta)
	return s.completeAppWebhookDelivery(ctx, id, currentAttempt, claimUntil, "dead", responseCode, errMsg, time.Time{}, time.Time{}, meta)
}

// The subscription lock, delivery UPDATE, attempt INSERT, and receiver state
// UPDATE are one SQL statement. Claims take locks in the same order. A stale
// claim produces no updated row and cannot change the receiver state.
func (s *PgStore) completeAppWebhookDelivery(ctx context.Context, id string, currentAttempt int, claimUntil time.Time, outcome string, responseCode int, errMsg string, nextAttemptAt, deliveredAt time.Time, meta []AppWebhookAttemptMetadata) error {
	started, finished, _ := appWebhookAttemptTimes(meta)
	var receiverCooldownUntil any
	var receiverCooldownTargetURL string
	if len(meta) > 0 && meta[0].ReceiverCooldownUntil != nil {
		receiverCooldownUntil = *meta[0].ReceiverCooldownUntil
		receiverCooldownTargetURL = meta[0].ReceiverCooldownTargetURL
	}
	status := outcome
	var attemptNextAt any
	var deliveryNextAt any = time.Unix(0, 0).UTC()
	if outcome == "retrying" {
		status = "pending"
		attemptNextAt = nextAttemptAt
		deliveryNextAt = nextAttemptAt
	}
	var delivered any
	if outcome == "succeeded" {
		delivered = deliveredAt
	}
	// A permanent client rejection shows the receiver is responding, so the
	// next queued delivery can use normal capacity. Transient failures without
	// Retry-After wait before another recovery probe is allowed.
	reopen := outcome == "succeeded" || (outcome == "dead" && responseCode >= 400 && responseCode < 500 && responseCode != 429)
	fallbackUntil := finished.Add(AppWebhookRecoveryRetryDelay)
	tag, err := s.pool.Exec(ctx, `
		with locked_hook as materialized (
			select w.id
			  from app_webhooks w
			  join app_webhook_deliveries d on d.webhook_id = w.id
			 where d.id = $1
			 for update of w
		), updated as (
			update app_webhook_deliveries d set
				status = $4, last_response_code = $5, last_error = $6,
				attempt = $3 + 1, next_attempt_at = $7,
				delivered_at = coalesce($8::timestamptz, delivered_at),
				updated_at = $9
			from locked_hook h
			where d.id = $1 and d.webhook_id = h.id
			  and d.status = 'in_flight' and d.attempt = $3
			  and d.next_attempt_at = $2
			returning d.id, d.webhook_id, d.replay_generation
		), receiver_state as (
			update app_webhooks w
			   set receiver_cooldown_until = case
			       when w.receiver_recovery_probe_delivery_id = u.id and $15::boolean
			           then case when w.receiver_cooldown_until <= $9 then null else w.receiver_cooldown_until end
			       when $13::timestamptz is not null and w.target_url = $14
			           then greatest(w.receiver_cooldown_until, $13::timestamptz)
			       when w.receiver_recovery_probe_delivery_id = u.id
			           then greatest(w.receiver_cooldown_until, $16::timestamptz)
			       else w.receiver_cooldown_until end,
			       receiver_recovery_probe_delivery_id = case
			           when w.receiver_recovery_probe_delivery_id = u.id then null
			           else w.receiver_recovery_probe_delivery_id end
			  from updated u
			 where w.id = u.webhook_id
			   and (w.receiver_recovery_probe_delivery_id = u.id or
			       ($13::timestamptz is not null and w.target_url = $14 and
			        (w.receiver_cooldown_until is null or w.receiver_cooldown_until < $13::timestamptz)))
		)
		insert into app_webhook_delivery_attempts
			(delivery_id, replay_generation, attempt_number, outcome,
			 response_code, error, started_at, finished_at, next_attempt_at)
		select id, replay_generation, $3 + 1, $10, $5, $6, $11, $9, $12
		  from updated
	`, id, claimUntil, currentAttempt, status, responseCode, errMsg,
		deliveryNextAt, delivered, finished, outcome, started, attemptNextAt, receiverCooldownUntil, receiverCooldownTargetURL, reopen, fallbackUntil)
	if err != nil {
		return fmt.Errorf("state: complete app webhook delivery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.appWebhookMarkMiss(ctx, id)
	}
	return nil
}

func (s *PgStore) appWebhookMarkMiss(ctx context.Context, id string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`select exists(select 1 from app_webhook_deliveries where id = $1)`, id,
	).Scan(&exists); err != nil {
		return fmt.Errorf("state: probe app webhook delivery claim: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
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
			replay_generation = replay_generation + 1,
			last_error = '',
			last_response_code = 0,
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

func (s *PgStore) ListAppWebhookDeliveryAttempts(ctx context.Context, deliveryID, webhookID, accountID string, pageSize int, pageToken string) ([]AppWebhookDeliveryAttempt, string, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	generation, number := -1, 0
	if pageToken != "" {
		var ok bool
		generation, number, ok = decodeAppWebhookAttemptPageToken(pageToken)
		if !ok {
			return nil, "", ErrInvalidAppWebhookAttemptPageToken
		}
	}
	rows, err := s.pool.Query(ctx, `
		select a.id, a.delivery_id, a.replay_generation, a.attempt_number,
		       a.outcome, a.response_code, a.error, a.started_at,
		       a.finished_at, a.next_attempt_at
		  from app_webhook_delivery_attempts a
		  join app_webhook_deliveries d on d.id = a.delivery_id
		 where d.id = $1 and d.webhook_id = $2 and d.account_id = $3
		   and ($4::integer < 0 or (a.replay_generation, a.attempt_number) < ($4, $5))
		 order by a.replay_generation desc, a.attempt_number desc
		 limit $6
	`, deliveryID, webhookID, accountID, generation, number, pageSize+1)
	if err != nil {
		return nil, "", fmt.Errorf("state: list webhook attempts: %w", err)
	}
	defer rows.Close()
	var out []AppWebhookDeliveryAttempt
	for rows.Next() {
		var a AppWebhookDeliveryAttempt
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.ReplayGeneration, &a.AttemptNumber,
			&a.Outcome, &a.ResponseCode, &a.Error, &a.StartedAt, &a.FinishedAt, &a.NextAttemptAt); err != nil {
			return nil, "", fmt.Errorf("state: scan webhook attempt: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("state: read webhook attempts: %w", err)
	}
	var nextToken string
	if len(out) > pageSize {
		nextToken = encodeAppWebhookAttemptPageToken(out[pageSize-1])
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (s *PgStore) AppWebhookDeliveryHealth(ctx context.Context, webhookID, accountID string, now time.Time) (AppWebhookDeliveryHealth, error) {
	var health AppWebhookDeliveryHealth
	var oldest, receiverCooldown pgtype.Timestamptz
	var receiverState string
	err := s.pool.QueryRow(ctx, `
		select w.id,
		       count(d.id) filter (where d.status = 'pending'),
		       count(d.id) filter (where d.status = 'in_flight'),
		       count(d.id) filter (where d.status = 'dead'),
		       case
		           when w.receiver_cooldown_until > $3 then null
		           when count(d.id) filter (where d.status = 'in_flight' and d.next_attempt_at > $3) >=
		               case when w.receiver_cooldown_until is null then $5 else 1 end then null
		           else min(d.next_attempt_at) filter (where d.status in ('pending', 'in_flight') and d.next_attempt_at <= $3)
		       end,
		       count(d.id) filter (where d.status = 'succeeded' and d.delivered_at >= $4),
		       count(d.id) filter (where d.status = 'dead' and d.updated_at >= $4),
		       max(w.receiver_cooldown_until) filter (where w.receiver_cooldown_until > $3),
		       case
		           when w.receiver_cooldown_until is null then 'ready'
		           when w.receiver_cooldown_until > $3 then 'cooling_down'
		           when count(d.id) filter (where d.id = w.receiver_recovery_probe_delivery_id
		               and d.status = 'in_flight' and d.next_attempt_at > $3) > 0 then 'probing'
		           else 'awaiting_probe'
		       end
		  from app_webhooks w
		  left join app_webhook_deliveries d on d.webhook_id = w.id and d.account_id = w.account_id
		 where w.id = $1 and w.account_id = $2
		 group by w.id
	`, webhookID, accountID, now, now.Add(-24*time.Hour), AppWebhookMaxInFlightPerSubscription).Scan(
		&health.WebhookID, &health.PendingCount, &health.InFlightCount, &health.DeadCount,
		&oldest, &health.RecentSucceededCount, &health.RecentDeadCount, &receiverCooldown, &receiverState)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppWebhookDeliveryHealth{}, ErrNotFound
	}
	if err != nil {
		return AppWebhookDeliveryHealth{}, fmt.Errorf("state: webhook delivery health: %w", err)
	}
	if oldest.Valid {
		at := oldest.Time
		health.OldestOverdueAt = &at
	}
	if receiverCooldown.Valid {
		at := receiverCooldown.Time
		health.ReceiverCooldownUntil = &at
	}
	health.ReceiverState = AppWebhookReceiverState(receiverState)
	return health, nil
}

func (s *PgStore) OldestOverdueAppWebhookDeliveryAt(ctx context.Context, now time.Time) (*time.Time, error) {
	var oldest pgtype.Timestamptz
	if err := s.pool.QueryRow(ctx, `
		with live_claims as materialized (
			select webhook_id, count(*) as n
			  from app_webhook_deliveries
			 where status = 'in_flight' and next_attempt_at > $1
			 group by webhook_id
		)
		select min(d.next_attempt_at)
		  from app_webhook_deliveries d
		  join app_webhooks w on w.id = d.webhook_id
		  left join live_claims live on live.webhook_id = d.webhook_id
		 where d.status in ('pending', 'in_flight') and d.next_attempt_at <= $1
		   and (w.receiver_cooldown_until is null or w.receiver_cooldown_until <= $1)
		   and coalesce(live.n, 0) < case when w.receiver_cooldown_until is null then $2 else 1 end
	`, now, AppWebhookMaxInFlightPerSubscription).Scan(&oldest); err != nil {
		return nil, fmt.Errorf("state: oldest overdue webhook delivery: %w", err)
	}
	if !oldest.Valid {
		return nil, nil
	}
	at := oldest.Time
	return &at, nil
}

func (s *PgStore) PruneAppWebhookDeliveries(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	// A dead delivery can also have a unified dead-letter projection containing
	// a copy of its payload. Lock that row first, matching unified replay's lock
	// order, then lock the delivery. Skip either when busy so replay wins the
	// race without deadlocking or leaving a stale projection behind. Select
	// twice the deletion limit so a few locked rows do not stall a whole pass.
	tag, err := s.pool.Exec(ctx, `
		with candidates as materialized (
			select id from app_webhook_deliveries
			 where status in ('succeeded', 'dead') and updated_at < $1
			 order by updated_at, id
			 limit ($2 * 2)
		), locked_projection as materialized (
			select e.source_id from dead_letter_events e
			 join candidates c on c.id = e.source_id
			 where e.source = 'webhook_delivery'
			 for update of e skip locked
		), doomed as materialized (
			select d.id from app_webhook_deliveries d
			 join candidates c on c.id = d.id
			 left join dead_letter_events e
			   on e.source = 'webhook_delivery' and e.source_id = d.id
			 where d.status in ('succeeded', 'dead') and d.updated_at < $1
			   and (e.id is null or e.source_id in (select source_id from locked_projection))
			 order by d.updated_at, d.id
			 limit $2
			 for update of d skip locked
		), deleted_projection as (
			delete from dead_letter_events e using doomed
			 where e.source = 'webhook_delivery' and e.source_id = doomed.id
			 returning e.id
		)
		delete from app_webhook_deliveries d using doomed
		 where d.id = doomed.id
	`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("state: prune webhook deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *PgStore) AppWebhookDeliveryStorageBytes(ctx context.Context) (int64, error) {
	var bytes int64
	err := s.pool.QueryRow(ctx, `
		select pg_total_relation_size('app_webhook_deliveries'::regclass)
		     + pg_total_relation_size('app_webhook_delivery_attempts'::regclass)
	`).Scan(&bytes)
	if err != nil {
		return 0, fmt.Errorf("state: webhook delivery storage size: %w", err)
	}
	return bytes, nil
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
		w              AppWebhook
		appID          pgtype.Text
		platformTenant pgtype.Text
		scope          string
		filter         []string
		retry          string
		format         string
	)
	err := s.Scan(
		&w.ID, &appID, &platformTenant, &w.AccountID, &scope, &w.TargetURL, &w.SecretSealed,
		&filter, &retry, &format, &w.Enabled, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return AppWebhook{}, err
	}
	if appID.Valid {
		w.AppID = appID.String
	}
	if platformTenant.Valid {
		w.PlatformTenantID = platformTenant.String
	}
	w.Scope = AppWebhookScope(scope)
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
		appID      pgtype.Text
		payload    []byte
		status     string
		event      string
		lastErr    *string
		lastRespCo *int32
	)
	err := s.Scan(
		&d.ID, &d.WebhookID, &appID, &d.AccountID, &event, &payload,
		&d.Attempt, &status, &lastErr, &lastRespCo,
		&d.NextAttemptAt, &d.DeliveredAt, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return AppWebhookDelivery{}, err
	}
	if appID.Valid {
		d.AppID = appID.String
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
