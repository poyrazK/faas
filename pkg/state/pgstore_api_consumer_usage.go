package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RecordAPIConsumerUsage writes the idempotency event and its minute
// aggregate in one transaction. A retried event returns applied=false and
// does not increment the aggregate a second time.
func (s *PgStore) RecordAPIConsumerUsage(ctx context.Context, event APIConsumerUsageEvent) (bool, error) {
	if err := ValidateAPIConsumerUsageEvent(event); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		insert into api_consumer_usage_events
		       (event_id, account_id, app_id, consumer_key, window_start,
		        request_count, error_count, billable_units, platform_tenant_id)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, nullif($9::text, '')::uuid)
		on conflict (event_id) do nothing
		returning true`,
		event.EventID, event.AccountID, event.AppID, event.ConsumerKey,
		event.WindowStart.UTC(), event.RequestCount, event.ErrorCount, event.BillableUnits, event.PlatformTenantID,
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// An older apid may have committed usage but ignored the new audit
		// envelope. Replay must fill the missing exact record without applying
		// the financial increment twice.
		if event.Audit != nil {
			if err := insertRequestAuditTx(ctx, tx, event); err != nil {
				return false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if event.PlatformTenantID != "" {
		_, err = tx.Exec(ctx, `
			insert into platform_tenant_usage_minutes
			       (account_id, platform_tenant_id, app_id, consumer_key, window_start,
			        request_count, error_count, billable_units)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
			on conflict (account_id, platform_tenant_id, app_id, consumer_key, window_start) do update
			set request_count = platform_tenant_usage_minutes.request_count + excluded.request_count,
			    error_count = platform_tenant_usage_minutes.error_count + excluded.error_count,
			    billable_units = platform_tenant_usage_minutes.billable_units + excluded.billable_units,
			    updated_at = now()`,
			event.AccountID, event.PlatformTenantID, event.AppID, event.ConsumerKey, event.WindowStart.UTC(),
			event.RequestCount, event.ErrorCount, event.BillableUnits)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(ctx, `
		insert into api_consumer_usage_minutes
		       (account_id, app_id, consumer_key, window_start,
		        request_count, error_count, billable_units)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
		on conflict (account_id, app_id, consumer_key, window_start) do update
		set request_count = api_consumer_usage_minutes.request_count + excluded.request_count,
		    error_count = api_consumer_usage_minutes.error_count + excluded.error_count,
		    billable_units = api_consumer_usage_minutes.billable_units + excluded.billable_units,
		    updated_at = now()`,
		event.AccountID, event.AppID, event.ConsumerKey, event.WindowStart.UTC(),
		event.RequestCount, event.ErrorCount, event.BillableUnits,
	)
	if err != nil {
		return false, err
	}
	if event.Audit != nil {
		if err := insertRequestAuditTx(ctx, tx, event); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return inserted, nil
}

// insertRequestAuditTx is idempotent but refuses a reused event ID from a
// different account/app. The matching usage row already exists in this tx.
func insertRequestAuditTx(ctx context.Context, tx pgx.Tx, event APIConsumerUsageEvent) error {
	audit := event.Audit
	if audit == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `
		insert into request_audit_events
		       (event_id, account_id, app_id, consumer_key, platform_tenant_id,
		        route_template, method, http_status, latency_ms, trace_id,
		        deployment_id, commit_sha, occurred_at, request_id, source_ip)
		select u.event_id, u.account_id, u.app_id, u.consumer_key, u.platform_tenant_id,
		       $4, $5, $6, $7, $8, nullif($9::text, '')::uuid, $10, $11, $12,
		       nullif($13::text, '')::inet
		  from api_consumer_usage_events u
		  join apps a on a.id = u.app_id and a.account_id = u.account_id
		 where u.event_id = $1::uuid and u.account_id = $2::uuid and u.app_id = $3::uuid
		on conflict (event_id) do nothing`,
		event.EventID, event.AccountID, event.AppID,
		audit.RouteTemplate, audit.Method, audit.HTTPStatus, audit.LatencyMS,
		audit.TraceID, audit.DeploymentID, audit.CommitSHA, audit.OccurredAt.UTC(), audit.RequestID, audit.SourceIP)
	if err != nil {
		return err
	}
	var matched bool
	if err := tx.QueryRow(ctx, `select exists (
		select 1 from request_audit_events
		where event_id = $1::uuid and account_id = $2::uuid and app_id = $3::uuid)`,
		event.EventID, event.AccountID, event.AppID).Scan(&matched); err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("request audit: event ID belongs to a different account or app")
	}
	return nil
}

func (s *PgStore) ListAPIConsumerUsage(ctx context.Context, accountID, appID, consumerKey string, since, until time.Time) ([]APIConsumerUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select account_id, app_id, consumer_key, window_start,
		       request_count, error_count, billable_units
		  from api_consumer_usage_minutes
		 where account_id = $1::uuid
		   and app_id = $2::uuid
		   and consumer_key = $3
		   and window_start >= $4
		   and window_start < $5
		 order by window_start asc`, accountID, appID, consumerKey, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerUsageBucket
	for rows.Next() {
		var bucket APIConsumerUsageBucket
		if err := rows.Scan(&bucket.AccountID, &bucket.AppID, &bucket.ConsumerKey,
			&bucket.WindowStart, &bucket.RequestCount, &bucket.ErrorCount, &bucket.BillableUnits); err != nil {
			return nil, err
		}
		bucket.WindowStart = bucket.WindowStart.UTC()
		out = append(out, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list api consumer usage: %w", err)
	}
	return out, nil
}
