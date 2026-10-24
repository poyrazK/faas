package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ LogEventStore = (*PgStore)(nil)

type logEventScanner interface {
	Scan(dest ...any) error
}

type logEventQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scanLogEvent(row logEventScanner) (LogEvent, error) {
	var (
		event                                 LogEvent
		deploymentID                          pgtype.UUID
		instanceID, sourceEventID, requestID  pgtype.Text
		traceID, route, method, level, stream pgtype.Text
		status, latencyMS                     pgtype.Int4
		source                                string
		fields                                []byte
	)
	if err := row.Scan(
		&event.ID,
		&event.OccurredAt,
		&event.AccountID,
		&event.AppID,
		&deploymentID,
		&instanceID,
		&source,
		&sourceEventID,
		&requestID,
		&traceID,
		&route,
		&method,
		&status,
		&level,
		&stream,
		&event.Message,
		&latencyMS,
		&event.Occurrences,
		&event.ColdBoot,
		&fields,
	); err != nil {
		return LogEvent{}, err
	}
	if deploymentID.Valid {
		event.DeploymentID = uuid.UUID(deploymentID.Bytes).String()
	}
	event.Source = LogEventSource(source)
	if instanceID.Valid {
		event.InstanceID = instanceID.String
	}
	if sourceEventID.Valid {
		event.SourceEventID = sourceEventID.String
	}
	if requestID.Valid {
		event.RequestID = requestID.String
	}
	if traceID.Valid {
		event.TraceID = traceID.String
	}
	if route.Valid {
		event.Route = route.String
	}
	if method.Valid {
		event.Method = method.String
	}
	if status.Valid {
		event.Status = int(status.Int32)
	}
	if level.Valid {
		event.Level = level.String
	}
	if stream.Valid {
		event.Stream = stream.String
	}
	if latencyMS.Valid {
		latency := int(latencyMS.Int32)
		event.LatencyMS = &latency
	}
	event.Fields = append(json.RawMessage(nil), fields...)
	event.OccurredAt = event.OccurredAt.UTC()
	return event, nil
}

// InsertLogEvent writes one idempotent source projection. The conflict target
// mirrors log_events_source_dedupe_idx; an identical producer retry returns
// the original row instead of creating a duplicate customer event.
func (s *PgStore) InsertLogEvent(ctx context.Context, event LogEvent) (LogEvent, error) {
	normalized, err := normalizeLogEventForInsert(event, time.Now())
	if err != nil {
		return LogEvent{}, err
	}
	return insertLogEventRow(ctx, s.pool, normalized)
}

// insertLogEventRow also accepts a pgx transaction, so a source projection
// and its authoritative row can commit or roll back together.
func insertLogEventRow(ctx context.Context, db logEventQueryRower, normalized LogEvent) (LogEvent, error) {
	var latency any
	if normalized.LatencyMS != nil {
		latency = *normalized.LatencyMS
	}
	row := db.QueryRow(ctx, `
		insert into log_events (
			id, occurred_at, account_id, app_id, deployment_id, instance_id,
			source, source_event_id, request_id, trace_id, route, method,
			status, level, stream, message, latency_ms, occurrences, cold_boot, fields
		) values (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20::jsonb
		)
		on conflict (account_id, app_id, source, source_event_id, occurred_at)
			where source_event_id is not null
		do update set source_event_id = excluded.source_event_id
		returning id, occurred_at, account_id, app_id, deployment_id, instance_id,
			source, source_event_id, request_id, trace_id, route, method,
			status, level, stream, message, latency_ms, occurrences, cold_boot, fields`,
		normalized.ID,
		normalized.OccurredAt,
		normalized.AccountID,
		normalized.AppID,
		nullIfEmpty(normalized.DeploymentID),
		nullIfEmpty(normalized.InstanceID),
		string(normalized.Source),
		nullIfEmpty(normalized.SourceEventID),
		nullIfEmpty(normalized.RequestID),
		nullIfEmpty(normalized.TraceID),
		nullIfEmpty(normalized.Route),
		nullIfEmpty(normalized.Method),
		nullableInt(normalized.Status),
		nullIfEmpty(normalized.Level),
		nullIfEmpty(normalized.Stream),
		normalized.Message,
		latency,
		normalized.Occurrences,
		normalized.ColdBoot,
		[]byte(normalized.Fields),
	)
	inserted, err := scanLogEvent(row)
	if err != nil {
		return LogEvent{}, mapErr(err)
	}
	return inserted, nil
}

// logEventFilterSQL is the shared tenant predicate for ledger reads; its
// parameters are logEventFilterArgs. Both account_id and app_id stay in the
// predicate even though app ids are globally unique: the duplicate tenant key
// is the persistence-level IDOR guard required by ADR-213.
const logEventFilterSQL = `
		 where account_id = $1
		   and app_id = $2
		   and occurred_at >= $3
		   and occurred_at < $4
		   and ($5::text = '' or source = $5)
		   and ($6::uuid is null or deployment_id = $6::uuid)
		   and ($7::text = '' or request_id = $7 or trace_id = $7)
		   and ($8::text = '' or trace_id = $8)
		   and ($9::text = '' or route = $9)
		   and ($10::int = 0 or status = $10)
		   and (coalesce(cardinality($11::text[]), 0) = 0 or level = any($11::text[]))
		   and ($12::int = 0 or (status >= $12 * 100 and status < $12 * 100 + 100))
		   and ($13::text = '' or message ilike $13 escape '\')`

// logEventLikeEscaper makes a customer query a literal ILIKE substring.
var logEventLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func logEventFilterArgs(f LogEventFilter) []any {
	pattern := ""
	if f.Query != "" {
		pattern = "%" + logEventLikeEscaper.Replace(f.Query) + "%"
	}
	levels := f.Levels
	if levels == nil {
		levels = []string{}
	}
	return []any{
		f.AccountID, f.AppID, f.Since, f.Until, string(f.Source), nullIfEmpty(f.DeploymentID),
		f.RequestID, f.TraceID, f.Route, f.Status, levels, f.StatusClass, pattern,
	}
}

type logEventQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// withLogEventTimeout runs fn directly, or inside a read-only transaction whose
// statement_timeout is the filter's bound so the limit dies with the read.
func (s *PgStore) withLogEventTimeout(ctx context.Context, timeout time.Duration, fn func(logEventQuerier) error) error {
	if timeout <= 0 {
		return fn(s.pool)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `select set_config('statement_timeout', $1, true)`, strconv.FormatInt(timeout.Milliseconds(), 10)); err != nil {
		return mapErr(err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

// logEventReadErr maps a cancelled statement to ErrLogSearchTimeout.
func logEventReadErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return ErrLogSearchTimeout
	}
	return mapErr(err)
}

// ListLogEvents returns one newest-first tenant page and one-row lookahead.
func (s *PgStore) ListLogEvents(ctx context.Context, filter LogEventFilter) ([]LogEvent, bool, error) {
	normalized, err := normalizeLogEventFilter(filter)
	if err != nil {
		return nil, false, err
	}
	hasCursor := !normalized.BeforeAt.IsZero()
	var beforeAt, beforeID any
	if hasCursor {
		beforeAt = normalized.BeforeAt
		beforeID = normalized.BeforeID
	}
	args := append(logEventFilterArgs(normalized), hasCursor, beforeAt, beforeID, normalized.Limit+1)
	events := make([]LogEvent, 0, normalized.Limit+1)
	err = s.withLogEventTimeout(ctx, normalized.Timeout, func(q logEventQuerier) error {
		rows, err := q.Query(ctx, `
		select id, occurred_at, account_id, app_id, deployment_id, instance_id,
		       source, source_event_id, request_id, trace_id, route, method,
		       status, level, stream, message, latency_ms, occurrences, cold_boot, fields
		  from log_events`+logEventFilterSQL+`
		   and (not $14::boolean or (occurred_at, id) < ($15::timestamptz, $16::uuid))
		 order by occurred_at desc, id desc
		 limit $17`, args...)
		if err != nil {
			return logEventReadErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			event, scanErr := scanLogEvent(rows)
			if scanErr != nil {
				return scanErr
			}
			events = append(events, event)
		}
		return logEventReadErr(rows.Err())
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(events) > normalized.Limit
	if hasMore {
		events = events[:normalized.Limit]
	}
	return events, hasMore, nil
}

// FacetLogEvents counts the filtered window in one scan: the CTE is
// materialized once and each facet groups it.
func (s *PgStore) FacetLogEvents(ctx context.Context, filter LogEventFilter, routeLimit int) (LogEventFacets, error) {
	normalized, err := normalizeLogEventFilter(filter)
	if err != nil {
		return LogEventFacets{}, err
	}
	if routeLimit < 1 {
		routeLimit = 1
	}
	args := append(logEventFilterArgs(normalized), routeLimit+1)
	var facets LogEventFacets
	err = s.withLogEventTimeout(ctx, normalized.Timeout, func(q logEventQuerier) error {
		rows, err := q.Query(ctx, `
		with m as materialized (
		  select source, level, status, route, occurrences
		    from log_events`+logEventFilterSQL+`
		)
		select 'total', '', coalesce(sum(occurrences), 0)::bigint from m
		union all
		select 'source', source, sum(occurrences)::bigint from m group by source
		union all
		select 'level', level, sum(occurrences)::bigint from m where level is not null group by level
		union all
		select 'status', (status / 100)::text || 'xx', sum(occurrences)::bigint from m where status is not null group by status / 100
		union all
		(select 'route', route, sum(occurrences)::bigint from m where route is not null group by route order by 3 desc, 2 limit $14)`, args...)
		if err != nil {
			return logEventReadErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			var dim, value string
			var count int64
			if err := rows.Scan(&dim, &value, &count); err != nil {
				return mapErr(err)
			}
			facets.add(dim, value, count)
		}
		return logEventReadErr(rows.Err())
	})
	if err != nil {
		return LogEventFacets{}, err
	}
	facets.finish(routeLimit)
	return facets, nil
}

// ListAccountTraceLogEvents returns the newest events for one exact trace id
// across a set of the account's apps in one round trip. The equality on
// trace_id keeps the read on log_events_app_trace_time_idx; the per-app
// ListLogEvents form cannot, because its optional filters are written as
// catch-all predicates.
func (s *PgStore) ListAccountTraceLogEvents(ctx context.Context, filter AccountTraceLogFilter) ([]LogEvent, bool, error) {
	normalized, err := normalizeAccountTraceLogFilter(filter)
	if err != nil {
		return nil, false, err
	}
	if len(normalized.AppIDs) == 0 {
		return nil, false, nil
	}
	rows, err := s.pool.Query(ctx, `
		select id, occurred_at, account_id, app_id, deployment_id, instance_id,
		       source, source_event_id, request_id, trace_id, route, method,
		       status, level, stream, message, latency_ms, occurrences, cold_boot, fields
		  from log_events
		 where account_id = $1
		   and app_id = any($2::uuid[])
		   and trace_id = $3
		   and ($4::text = '' or source = $4)
		   and occurred_at >= $5
		   and occurred_at < $6
		 order by occurred_at desc, id desc
		 limit $7`,
		normalized.AccountID,
		normalized.AppIDs,
		normalized.TraceID,
		string(normalized.Source),
		normalized.Since,
		normalized.Until,
		normalized.Limit+1,
	)
	if err != nil {
		return nil, false, mapErr(err)
	}
	defer rows.Close()
	events := make([]LogEvent, 0, normalized.Limit+1)
	for rows.Next() {
		event, scanErr := scanLogEvent(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, false, mapErr(err)
	}
	hasMore := len(events) > normalized.Limit
	if hasMore {
		events = events[:normalized.Limit]
	}
	return events, hasMore, nil
}
