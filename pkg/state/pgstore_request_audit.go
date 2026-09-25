package state

import (
	"context"
	"time"
)

func (s *PgStore) ListRequestAudit(ctx context.Context, accountID, appID string, since, until time.Time, limit int) ([]RequestAuditRecord, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		select event_id::text, account_id::text, app_id::text,
		       case when consumer_key = '__anonymous__' then '' else consumer_key end,
		       coalesce(platform_tenant_id::text, ''), route_template, method,
		       http_status, latency_ms, trace_id, coalesce(deployment_id::text, ''),
		       commit_sha, occurred_at, request_id, coalesce(host(source_ip), '')
		from request_audit_events
		where account_id = $1::uuid and app_id = $2::uuid
		  and occurred_at >= $3 and occurred_at < $4
		order by occurred_at desc, event_id desc
		limit $5`, accountID, appID, since.UTC(), until.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RequestAuditRecord, 0)
	for rows.Next() {
		var r RequestAuditRecord
		if err := rows.Scan(&r.EventID, &r.AccountID, &r.AppID, &r.ConsumerID,
			&r.PlatformTenantID, &r.RouteTemplate, &r.Method, &r.HTTPStatus,
			&r.LatencyMS, &r.TraceID, &r.DeploymentID, &r.CommitSHA,
			&r.OccurredAt, &r.RequestID, &r.SourceIP); err != nil {
			return nil, err
		}
		r.OccurredAt = r.OccurredAt.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PgStore) ListDiscoveredAuditRoutes(ctx context.Context, accountID, appID string, limit int) ([]string, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		select route_template from request_audit_events
		where account_id = $1::uuid and app_id = $2::uuid
		  and route_template <> '__route_other__'
		group by route_template order by route_template limit $3`, accountID, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var route string
		if err := rows.Scan(&route); err != nil {
			return nil, err
		}
		out = append(out, route)
	}
	return out, rows.Err()
}
