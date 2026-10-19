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
		        request_count, error_count, billable_units, platform_tenant_id, platform_tenant_surface_id,
		        platform_tenant_jwt_authorization_rule_id)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8,
		        nullif($9::text, '')::uuid, nullif($10::text, '')::uuid, nullif($11::text, '')::uuid)
		on conflict (event_id) do nothing
		returning true`,
		event.EventID, event.AccountID, event.AppID, event.ConsumerKey,
		event.WindowStart.UTC(), event.RequestCount, event.ErrorCount, event.BillableUnits,
		event.PlatformTenantID, event.PlatformTenantSurfaceID, event.PlatformTenantJWTAuthorizationRuleID,
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// An older apid may have committed usage but ignored the new audit
		// envelope. Replay must fill the missing exact record without applying
		// the financial increment twice.
		if event.Audit != nil {
			if err := insertRequestAuditTx(ctx, tx, event); err != nil {
				return false, err
			}
		}
		if discoveredRouteFor(event) != "" {
			if err := recordDiscoveredRouteTx(ctx, tx, event); err != nil {
				return false, err
			}
		}
		if event.Audit != nil || discoveredRouteFor(event) != "" {
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
		subject, sourceKind := event.ConsumerKey, "consumer"
		if event.PlatformTenantSurfaceID != "" {
			subject, sourceKind = event.PlatformTenantSurfaceID, "surface"
		} else if event.PlatformTenantJWTAuthorizationRuleID != "" {
			subject, sourceKind = event.PlatformTenantJWTAuthorizationRuleID, "jwt"
		}
		_, err = tx.Exec(ctx, `
			insert into platform_tenant_usage_minutes
			       (account_id, platform_tenant_id, app_id, source_kind, consumer_key, window_start,
			        request_count, error_count, billable_units, compute_ms, response_bytes)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
			on conflict (account_id, platform_tenant_id, app_id, source_kind, consumer_key, window_start) do update
			set request_count = platform_tenant_usage_minutes.request_count + excluded.request_count,
			    error_count = platform_tenant_usage_minutes.error_count + excluded.error_count,
			    billable_units = platform_tenant_usage_minutes.billable_units + excluded.billable_units,
			    compute_ms = platform_tenant_usage_minutes.compute_ms + excluded.compute_ms,
			    response_bytes = platform_tenant_usage_minutes.response_bytes + excluded.response_bytes,
			    updated_at = now()`,
			event.AccountID, event.PlatformTenantID, event.AppID, sourceKind, subject, event.WindowStart.UTC(),
			event.RequestCount, event.ErrorCount, event.BillableUnits, event.ComputeMS, event.ResponseBytes)
		if err != nil {
			return false, err
		}
		if kind, sourceID, route := tenantSourceRouteFor(event); route != "" {
			_, err = tx.Exec(ctx, `
				insert into platform_tenant_source_route_usage_minutes
				       (account_id, platform_tenant_id, app_id, source_kind, source_id, route, window_start, billable_units)
				values ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8)
				on conflict (account_id, platform_tenant_id, app_id, source_kind, source_id, window_start, route) do update
				set billable_units = platform_tenant_source_route_usage_minutes.billable_units + excluded.billable_units,
				    updated_at = now()`,
				event.AccountID, event.PlatformTenantID, event.AppID, kind, sourceID, route, event.WindowStart.UTC(), event.BillableUnits)
			if err != nil {
				return false, err
			}
		}
	}
	_, err = tx.Exec(ctx, `
		insert into api_consumer_usage_minutes
		       (account_id, app_id, consumer_key, window_start,
		        request_count, error_count, billable_units, compute_ms, response_bytes)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
		on conflict (account_id, app_id, consumer_key, window_start) do update
		set request_count = api_consumer_usage_minutes.request_count + excluded.request_count,
		    error_count = api_consumer_usage_minutes.error_count + excluded.error_count,
		    billable_units = api_consumer_usage_minutes.billable_units + excluded.billable_units,
		    compute_ms = api_consumer_usage_minutes.compute_ms + excluded.compute_ms,
		    response_bytes = api_consumer_usage_minutes.response_bytes + excluded.response_bytes,
		    updated_at = now()`,
		event.AccountID, event.AppID, event.ConsumerKey, event.WindowStart.UTC(),
		event.RequestCount, event.ErrorCount, event.BillableUnits, event.ComputeMS, event.ResponseBytes,
	)
	if err != nil {
		return false, err
	}
	if err := recordMeterUnitsTx(ctx, tx, event); err != nil {
		return false, err
	}
	if route := billingRouteFor(event); route != "" {
		_, err = tx.Exec(ctx, `
			insert into api_consumer_route_usage_minutes
			       (account_id, app_id, consumer_key, route, window_start, billable_units)
			values ($1::uuid, $2::uuid, $3, $4, $5, $6)
			on conflict (account_id, app_id, consumer_key, window_start, route) do update
			set billable_units = api_consumer_route_usage_minutes.billable_units + excluded.billable_units,
			    updated_at = now()`,
			event.AccountID, event.AppID, event.ConsumerKey, route, event.WindowStart.UTC(), event.BillableUnits)
		if err != nil {
			return false, err
		}
	}
	if event.Audit != nil {
		if err := insertRequestAuditTx(ctx, tx, event); err != nil {
			return false, err
		}
	}
	if discoveredRouteFor(event) != "" {
		if err := recordDiscoveredRouteTx(ctx, tx, event); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return inserted, nil
}

func (s *PgStore) ListAPIConsumerRouteUsage(ctx context.Context, accountID, appID, consumerKey string, since, until time.Time) ([]APIConsumerRouteUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select window_start, route, billable_units
		  from api_consumer_route_usage_minutes
		 where account_id = $1::uuid and app_id = $2::uuid and consumer_key = $3
		   and window_start >= $4 and window_start < $5
		 order by window_start, route`, accountID, appID, consumerKey, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerRouteUsageBucket
	for rows.Next() {
		var bucket APIConsumerRouteUsageBucket
		if err := rows.Scan(&bucket.WindowStart, &bucket.Route, &bucket.BillableUnits); err != nil {
			return nil, err
		}
		bucket.WindowStart = bucket.WindowStart.UTC()
		out = append(out, bucket)
	}
	return out, rows.Err()
}

func (s *PgStore) ListPlatformTenantSourceRouteUsage(ctx context.Context, accountID, tenantID string, since, until time.Time) ([]PlatformTenantSourceRouteUsageBucket, error) {
	if !until.After(since) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select app_id::text, source_kind, source_id::text, window_start, route, billable_units
		  from platform_tenant_source_route_usage_minutes
		 where account_id = $1::uuid and platform_tenant_id = $2::uuid
		   and window_start >= $3 and window_start < $4
		 order by window_start, app_id, source_kind, source_id, route`, accountID, tenantID, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlatformTenantSourceRouteUsageBucket
	for rows.Next() {
		var b PlatformTenantSourceRouteUsageBucket
		if err := rows.Scan(&b.AppID, &b.SourceKind, &b.SourceID, &b.WindowStart, &b.Route, &b.BillableUnits); err != nil {
			return nil, err
		}
		b.WindowStart = b.WindowStart.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// insertRequestAuditTx is idempotent but refuses a reused event ID from a
// different account/app. The matching usage row already exists in this tx.
// recordMeterUnitsTx adds an event's app-reported units to its consumer's
// ledger (ADR-1318) and its tenant's (ADR-1319).
func recordMeterUnitsTx(ctx context.Context, tx pgx.Tx, event APIConsumerUsageEvent) error {
	kind, subject := tenantUsageSubject(event)
	for meter, units := range event.MeterUnits {
		if event.ConsumerKey != AnonymousConsumerKey {
			if _, err := tx.Exec(ctx, `
				insert into api_consumer_meter_usage_minutes (account_id, app_id, consumer_key, meter, window_start, units)
				values ($1::uuid, $2::uuid, $3, $4, $5, $6)
				on conflict (account_id, app_id, consumer_key, meter, window_start) do update
				set units = api_consumer_meter_usage_minutes.units + excluded.units, updated_at = now()`,
				event.AccountID, event.AppID, event.ConsumerKey, meter, event.WindowStart.UTC(), units); err != nil {
				return err
			}
		}
		if event.PlatformTenantID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			insert into platform_tenant_meter_usage_minutes
			       (account_id, platform_tenant_id, app_id, source_kind, subject_key, meter, window_start, units)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
			on conflict (account_id, platform_tenant_id, app_id, source_kind, subject_key, meter, window_start) do update
			set units = platform_tenant_meter_usage_minutes.units + excluded.units, updated_at = now()`,
			event.AccountID, event.PlatformTenantID, event.AppID, kind, subject, meter, event.WindowStart.UTC(), units); err != nil {
			return err
		}
	}
	return nil
}

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
		       request_count, error_count, billable_units, compute_ms, response_bytes
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
			&bucket.WindowStart, &bucket.RequestCount, &bucket.ErrorCount, &bucket.BillableUnits,
			&bucket.ComputeMS, &bucket.ResponseBytes); err != nil {
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
