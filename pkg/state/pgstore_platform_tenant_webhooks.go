package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func (s *PgStore) CreatePlatformTenantWebhookIfUnderQuota(ctx context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	if in.AccountID == "" || in.PlatformTenantID == "" || in.AppID != "" ||
		(in.Scope != "" && in.Scope != AppWebhookScopePlatformTenant) ||
		!validPlatformTenantWebhookFilter(in.EventFilter) {
		return AppWebhook{}, ErrInvalidAppWebhookScope
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppWebhook{}, fmt.Errorf("state: begin platform tenant webhook create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	var locked string
	if err := tx.QueryRow(ctx, `select id::text from accounts where id=$1 for update`, in.AccountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: lock webhook account %s: %w", in.AccountID, err)
	}
	if err := tx.QueryRow(ctx, `select id::text from platform_tenants where id=$1::uuid and account_id=$2 for update`, in.PlatformTenantID, in.AccountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWebhook{}, ErrNotFound
		}
		return AppWebhook{}, fmt.Errorf("state: lock platform tenant webhook owner %s: %w", in.PlatformTenantID, err)
	}
	var count int
	if err := tx.QueryRow(ctx, `
		select count(*) from app_webhooks w
		 left join apps a on a.id = w.app_id
		 where w.account_id = $1
		   and (w.scope in ('account', 'platform_tenant') or
		        (w.scope = 'app' and a.account_id = $1 and a.status <> 'deleted'))
	`, in.AccountID).Scan(&count); err != nil {
		return AppWebhook{}, fmt.Errorf("state: count webhooks for account %s: %w", in.AccountID, err)
	}
	if count >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{Scope: AppWebhookQuotaScopeAccount, Limit: limits.WebhookPerAccount, Observed: count}
	}
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	filter := in.EventFilter
	row := tx.QueryRow(ctx, `
		insert into app_webhooks
			(app_id, platform_tenant_id, account_id, scope, target_url, secret_sealed,
			 event_filter, retry_policy, delivery_format, enabled)
		values (null, $1::uuid, $2, 'platform_tenant', $3, $4, $5::text[], $6, $7, $8)
		returning id, app_id::text, platform_tenant_id::text, account_id, scope, target_url, secret_sealed,
		          event_filter, retry_policy, delivery_format, enabled, created_at, updated_at
	`, in.PlatformTenantID, in.AccountID, in.TargetURL, in.SecretSealed, filter,
		string(in.RetryPolicy), string(in.DeliveryFormat), in.Enabled)
	hook, err := scanAppWebhook(row)
	if err != nil {
		if isUniqueViolation(err) {
			return AppWebhook{}, ErrConflict
		}
		return AppWebhook{}, fmt.Errorf("state: insert platform tenant webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppWebhook{}, fmt.Errorf("state: commit platform tenant webhook: %w", err)
	}
	return hook, nil
}

func (s *PgStore) ListPlatformTenantWebhookDeliveries(ctx context.Context, accountID, tenantID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	query := `
		select d.id, d.webhook_id, d.app_id, d.account_id, d.event, d.payload,
		       d.attempt, d.status, d.last_error, d.last_response_code,
		       d.next_attempt_at, d.delivered_at, d.created_at, d.updated_at
		  from app_webhook_deliveries d
		  join app_webhooks h on h.id = d.webhook_id
		 where d.account_id=$1 and d.webhook_id=$2
		   and h.account_id=$1 and h.scope='platform_tenant' and h.platform_tenant_id=$3::uuid
	`
	args := []any{accountID, webhookID, tenantID}
	if pageToken != "" {
		ts, id, ok := decodePageToken(pageToken)
		if !ok {
			return nil, "", fmt.Errorf("state: invalid page token")
		}
		query += ` and (d.created_at, d.id) < ($4, $5)`
		args = append(args, ts, id)
	}
	query += ` order by d.created_at desc, d.id desc limit $` + fmt.Sprint(len(args)+1)
	args = append(args, pageSize+1)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("state: list platform tenant webhook deliveries: %w", err)
	}
	defer rows.Close()
	out, err := scanAppWebhookDeliveries(rows)
	if err != nil {
		return nil, "", fmt.Errorf("state: scan platform tenant webhook deliveries: %w", err)
	}
	var next string
	if len(out) > pageSize {
		last := out[pageSize-1]
		next = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, next, nil
}
