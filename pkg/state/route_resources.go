package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/routeresources"
)

// InstanceResourceMinuteRow is one instance-minute of app cgroup usage with
// the identity vmmd stamped on it (ADR-20261121170000).
type InstanceResourceMinuteRow struct {
	AccountID    uuid.UUID
	DeploymentID uuid.UUID
	routeresources.InstanceMinute
}

// InstanceRouteMinuteRow is one route's activity on one instance-minute.
type InstanceRouteMinuteRow struct {
	AccountID uuid.UUID
	routeresources.RouteMinute
}

// RouteResourceStore keeps the minutes route resource peaks join. PgStore
// implements it; apid asserts it.
type RouteResourceStore interface {
	// AddInstanceResourceMinutes merges rows into their instance-minutes:
	// the first start stands, peaks and limits take the larger value, the
	// end takes the newer one and CPU time and samples add up.
	AddInstanceResourceMinutes(ctx context.Context, rows []InstanceResourceMinuteRow) error
	// AddInstanceRouteMinutes adds rows to their route minutes.
	AddInstanceRouteMinutes(ctx context.Context, rows []InstanceRouteMinuteRow) error
	// InstanceResourceMinutes returns an app's instance-minutes in
	// [from, to), newest first, at most limit; truncated reports more.
	InstanceResourceMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) (rows []routeresources.InstanceMinute, truncated bool, err error)
	// InstanceRouteMinutes returns an app's route minutes in [from, to).
	InstanceRouteMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) (rows []routeresources.RouteMinute, truncated bool, err error)
}

var _ RouteResourceStore = (*PgStore)(nil)

func (s *PgStore) AddInstanceResourceMinutes(ctx context.Context, rows []InstanceResourceMinuteRow) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows)
	apps, accounts, deployments := make([]uuid.UUID, n), make([]uuid.UUID, n), make([]uuid.UUID, n)
	instances, minutes := make([]string, n), make([]time.Time, n)
	samples, start, peak, end, limit := make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n)
	cpu, cpuPeak, cpuLimit := make([]int64, n), make([]int32, n), make([]int32, n)
	for i, r := range rows {
		apps[i], accounts[i], deployments[i] = r.AppID, r.AccountID, r.DeploymentID
		instances[i], minutes[i] = r.InstanceID, r.Minute.UTC().Truncate(time.Minute)
		samples[i], start[i], peak[i], end[i], limit[i] = int32(r.Samples), int32(r.MemStartMB), int32(r.MemPeakMB), int32(r.MemEndMB), int32(r.MemLimitMB)
		cpu[i], cpuPeak[i], cpuLimit[i] = r.CPUMS, int32(r.CPUPeakMillicores), int32(r.CPULimitMillicores)
	}
	// Rows of one batch never share a key: vmmd sends each
	// instance-minute once per batch.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_resource_minutes AS t
			(app_id, instance_id, minute, account_id, deployment_id, samples,
			 mem_start_mb, mem_peak_mb, mem_end_mb, mem_limit_mb,
			 cpu_ms, cpu_peak_millicores, cpu_limit_millicores)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::timestamptz[], $4::uuid[], $5::uuid[], $6::int[],
			$7::int[], $8::int[], $9::int[], $10::int[], $11::bigint[], $12::int[], $13::int[])
		ON CONFLICT (app_id, minute, instance_id) DO UPDATE SET
			samples = t.samples + EXCLUDED.samples,
			mem_peak_mb = GREATEST(t.mem_peak_mb, EXCLUDED.mem_peak_mb),
			mem_end_mb = LEAST(EXCLUDED.mem_end_mb, GREATEST(t.mem_peak_mb, EXCLUDED.mem_peak_mb)),
			mem_limit_mb = GREATEST(t.mem_limit_mb, EXCLUDED.mem_limit_mb),
			cpu_ms = t.cpu_ms + EXCLUDED.cpu_ms,
			cpu_peak_millicores = GREATEST(t.cpu_peak_millicores, EXCLUDED.cpu_peak_millicores),
			cpu_limit_millicores = GREATEST(t.cpu_limit_millicores, EXCLUDED.cpu_limit_millicores)`,
		apps, instances, minutes, accounts, deployments, samples, start, peak, end, limit, cpu, cpuPeak, cpuLimit)
	if err != nil {
		return fmt.Errorf("state: add instance resource minutes: %w", err)
	}
	return nil
}

func (s *PgStore) AddInstanceRouteMinutes(ctx context.Context, rows []InstanceRouteMinuteRow) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows)
	apps, accounts := make([]uuid.UUID, n), make([]uuid.UUID, n)
	instances, minutes, routes, methods := make([]string, n), make([]time.Time, n), make([]string, n), make([]string, n)
	requests, busy, maxLatency := make([]int64, n), make([]int64, n), make([]int32, n)
	for i, r := range rows {
		apps[i], accounts[i] = r.AppID, r.AccountID
		instances[i], minutes[i], routes[i], methods[i] = r.InstanceID, r.Minute.UTC().Truncate(time.Minute), r.Route, r.Method
		requests[i], busy[i], maxLatency[i] = r.Requests, r.BusyMS, int32(r.MaxLatencyMS)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO instance_route_minutes AS t
			(app_id, instance_id, minute, route, method, account_id, requests, busy_ms, max_latency_ms)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::timestamptz[], $4::text[], $5::text[], $6::uuid[],
			$7::bigint[], $8::bigint[], $9::int[])
		ON CONFLICT (app_id, minute, instance_id, route, method) DO UPDATE SET
			requests = t.requests + EXCLUDED.requests,
			busy_ms = t.busy_ms + EXCLUDED.busy_ms,
			max_latency_ms = GREATEST(t.max_latency_ms, EXCLUDED.max_latency_ms)`,
		apps, instances, minutes, routes, methods, accounts, requests, busy, maxLatency)
	if err != nil {
		return fmt.Errorf("state: add instance route minutes: %w", err)
	}
	return nil
}

func (s *PgStore) InstanceResourceMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) ([]routeresources.InstanceMinute, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, minute, samples, mem_start_mb, mem_peak_mb, mem_end_mb, mem_limit_mb,
		       cpu_ms, cpu_peak_millicores, cpu_limit_millicores
		FROM instance_resource_minutes
		WHERE app_id = $1 AND minute >= $2 AND minute < $3
		ORDER BY minute DESC, instance_id
		LIMIT $4`, appID, from, to, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("state: read instance resource minutes: %w", err)
	}
	defer rows.Close()
	var out []routeresources.InstanceMinute
	for rows.Next() {
		m := routeresources.InstanceMinute{AppID: appID}
		if err := rows.Scan(&m.InstanceID, &m.Minute, &m.Samples, &m.MemStartMB, &m.MemPeakMB, &m.MemEndMB, &m.MemLimitMB,
			&m.CPUMS, &m.CPUPeakMillicores, &m.CPULimitMillicores); err != nil {
			return nil, false, fmt.Errorf("state: scan instance resource minute: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("state: read instance resource minutes: %w", err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (s *PgStore) InstanceRouteMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) ([]routeresources.RouteMinute, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, minute, route, method, requests, busy_ms, max_latency_ms
		FROM instance_route_minutes
		WHERE app_id = $1 AND minute >= $2 AND minute < $3
		ORDER BY minute DESC, instance_id, route, method
		LIMIT $4`, appID, from, to, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("state: read instance route minutes: %w", err)
	}
	defer rows.Close()
	var out []routeresources.RouteMinute
	for rows.Next() {
		m := routeresources.RouteMinute{AppID: appID}
		if err := rows.Scan(&m.InstanceID, &m.Minute, &m.Route, &m.Method, &m.Requests, &m.BusyMS, &m.MaxLatencyMS); err != nil {
			return nil, false, fmt.Errorf("state: scan instance route minute: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("state: read instance route minutes: %w", err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}
