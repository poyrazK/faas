package state

import (
	"context"
	"time"
)

// StatusUptimeBuckets rolls terminal invocations into UTC calendar days. The
// generated day series keeps the public response stable at 30 points even on
// an idle box; days with no terminal traffic have zero counts and are treated
// as 100% by the API projection.
func (s *PgStore) StatusUptimeBuckets(ctx context.Context, since time.Time) ([]StatusUptimeBucket, error) {
	rows, err := s.pool.Query(ctx, `
		with days as (
			select generate_series(
				(($1::timestamptz at time zone 'UTC')::date)::timestamp,
				((now() at time zone 'UTC')::date)::timestamp,
				interval '1 day'
			) as day
		), counts as (
			select (created_at at time zone 'UTC')::date::timestamp as day,
			       count(*) filter (
					where outcome = 'success'
					   or (outcome is null and state = 'completed')
			       )::bigint as successful,
			       count(*) filter (
					where outcome is not null
					   or state in ('completed', 'failed', 'cancelled', 'dead_letter')
			       )::bigint as total
			  from invocations
			 where created_at >= $1
			   and (outcome is not null
				or state in ('completed', 'failed', 'cancelled', 'dead_letter'))
			 group by 1
		)
		select days.day,
		       coalesce(counts.successful, 0)::bigint,
		       coalesce(counts.total, 0)::bigint
		  from days
		  left join counts using (day)
		 order by days.day`, since.UTC())
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
