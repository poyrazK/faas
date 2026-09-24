package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// CreateAccountReleaseWebhookIfUnderQuota serializes with app webhook creation
// through the account row. The account cap counts both subscription scopes.
func (s *PgStore) CreateAccountReleaseWebhookIfUnderQuota(ctx context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	if in.AppID != "" || (in.Scope != "" && in.Scope != AppWebhookScopeAccount) ||
		!validAccountReleaseWebhookFilter(in.EventFilter) {
		return AppWebhook{}, ErrInvalidAppWebhookScope
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppWebhook{}, fmt.Errorf("state: begin account webhook create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from accounts where id=$1 for update`, in.AccountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: lock webhook account %s: %w", in.AccountID, err)
	}
	var count int
	if err := tx.QueryRow(ctx, `
		select count(*) from app_webhooks w
		 left join apps a on a.id = w.app_id
		 where w.account_id = $1
		   and (w.scope = 'account' or
		        (w.scope = 'app' and a.account_id = $1 and a.status <> 'deleted'))
	`, in.AccountID).Scan(&count); err != nil {
		return AppWebhook{}, fmt.Errorf("state: count webhooks for account %s: %w", in.AccountID, err)
	}
	if count >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope: AppWebhookQuotaScopeAccount, Limit: limits.WebhookPerAccount, Observed: count,
		}
	}
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	row := tx.QueryRow(ctx, `
		insert into app_webhooks
			(app_id, account_id, scope, target_url, secret_sealed,
			 event_filter, retry_policy, delivery_format, enabled)
		values (null, $1, 'account', $2, $3, $4::text[], $5, $6, $7)
		returning id, app_id::text, account_id, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled,
		          created_at, updated_at
	`, in.AccountID, in.TargetURL, in.SecretSealed, in.EventFilter,
		string(in.RetryPolicy), string(in.DeliveryFormat), in.Enabled)
	hook, err := scanAppWebhook(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppWebhook{}, ErrConflict
		}
		return AppWebhook{}, fmt.Errorf("state: insert account release webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppWebhook{}, fmt.Errorf("state: commit account release webhook: %w", err)
	}
	return hook, nil
}

// ListAccountReleaseWebhookDeliveries keeps both the account and subscription
// scope in the SQL predicate. Source apps are intentionally not filtered.
func (s *PgStore) ListAccountReleaseWebhookDeliveries(ctx context.Context, accountID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	var rows pgx.Rows
	var err error
	if pageToken == "" {
		rows, err = s.pool.Query(ctx, `
			select d.id, d.webhook_id, d.app_id, d.account_id, d.event, d.payload,
			       d.attempt, d.status, d.last_error, d.last_response_code,
			       d.next_attempt_at, d.delivered_at, d.created_at, d.updated_at
			  from app_webhook_deliveries d
			  join app_webhooks h on h.id = d.webhook_id
			 where d.account_id=$1 and d.webhook_id=$2
			   and h.account_id=$1 and h.scope='account'
			 order by d.created_at desc, d.id desc
			 limit $3
		`, accountID, webhookID, pageSize+1)
	} else {
		ts, id, ok := decodePageToken(pageToken)
		if !ok {
			return nil, "", fmt.Errorf("state: invalid page token")
		}
		rows, err = s.pool.Query(ctx, `
			select d.id, d.webhook_id, d.app_id, d.account_id, d.event, d.payload,
			       d.attempt, d.status, d.last_error, d.last_response_code,
			       d.next_attempt_at, d.delivered_at, d.created_at, d.updated_at
			  from app_webhook_deliveries d
			  join app_webhooks h on h.id = d.webhook_id
			 where d.account_id=$1 and d.webhook_id=$2
			   and h.account_id=$1 and h.scope='account'
			   and (d.created_at, d.id) < ($3, $4)
			 order by d.created_at desc, d.id desc
			 limit $5
		`, accountID, webhookID, ts, id, pageSize+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("state: list account webhook deliveries: %w", err)
	}
	defer rows.Close()
	out, err := scanAppWebhookDeliveries(rows)
	if err != nil {
		return nil, "", fmt.Errorf("state: scan account webhook deliveries: %w", err)
	}
	var next string
	if len(out) > pageSize {
		last := out[pageSize-1]
		next = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, next, nil
}
