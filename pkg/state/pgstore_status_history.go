package state

import (
	"context"
	"time"
)

// StatusUptimeBuckets rolls complete, platform-owned five-minute observations
// into UTC calendar days. A partially written interval is omitted, preserving
// the distinction between missing telemetry and a measured outage. Customer
// workload outcomes are intentionally absent from this query.
func (s *PgStore) StatusUptimeBuckets(ctx context.Context, since time.Time) ([]StatusUptimeBucket, error) {
	rows, err := s.pool.Query(ctx, `
		with complete_intervals as (
			select bucket_at,
			       bool_and(status in ('operational', 'maintenance')) as available
			  from status_observation_buckets
			 where bucket_at >= $1
			   and has_telemetry
			 group by bucket_at
			having count(distinct component) = 5
		)
		select (bucket_at at time zone 'UTC')::date::timestamp as day,
		       count(*) filter (where available)::bigint as successful,
		       count(*)::bigint as total
		  from complete_intervals
		 group by 1
		 order by 1`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusUptimeBucket
	for rows.Next() {
		var bucket StatusUptimeBucket
		if err := rows.Scan(&bucket.Day, &bucket.Successful, &bucket.Total); err != nil {
			return nil, err
		}
		bucket.Day = bucket.Day.UTC()
		out = append(out, bucket)
	}
	return out, rows.Err()
}

// ListStatusIncidentsSince returns recent incidents plus any still-open
// incident, newest first. The explicit cap keeps the public endpoint bounded
// even if an operator has accumulated a large historical ledger.
func (s *PgStore) ListStatusIncidentsSince(ctx context.Context, since time.Time, limit int) ([]StatusIncident, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		select id, component, severity, message, posted_at, resolved_at
		  from status_incidents
		 where posted_at >= $1
		    or resolved_at is null
		 order by posted_at desc, id desc
		 limit $2`, since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusIncident
	for rows.Next() {
		var inc StatusIncident
		if err := rows.Scan(&inc.ID, &inc.Component, &inc.Severity,
			&inc.Message, &inc.PostedAt, &inc.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}
