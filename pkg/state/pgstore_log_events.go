package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ LogEventStore = (*PgStore)(nil)

type logEventScanner interface {
	Scan(dest ...any) error
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
	var latency any
	if normalized.LatencyMS != nil {
		latency = *normalized.LatencyMS
	}
	row := s.pool.QueryRow(ctx, `
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

// ListLogEvents returns one newest-first tenant page and one-row lookahead.
// Both account_id and app_id stay in the SQL predicate even though app ids are
// globally unique: the duplicate tenant key is the persistence-level IDOR
// guard required by ADR-213.
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
	rows, err := s.pool.Query(ctx, `
		select id, occurred_at, account_id, app_id, deployment_id, instance_id,
		       source, source_event_id, request_id, trace_id, route, method,
		       status, level, stream, message, latency_ms, occurrences, cold_boot, fields
		  from log_events
		 where account_id = $1
		   and app_id = $2
		   and occurred_at >= $3
		   and occurred_at < $4
		   and ($5::text = '' or source = $5)
		   and ($6::uuid is null or deployment_id = $6::uuid)
		   and ($7::text = '' or request_id = $7 or trace_id = $7)
		   and ($8::text = '' or route = $8)
		   and ($9::int = 0 or status = $9)
		   and (not $10::boolean or (occurred_at, id) < ($11::timestamptz, $12::uuid))
		 order by occurred_at desc, id desc
		 limit $13`,
		normalized.AccountID,
		normalized.AppID,
		normalized.Since,
		normalized.Until,
		string(normalized.Source),
		nullIfEmpty(normalized.DeploymentID),
		normalized.RequestID,
		normalized.Route,
		normalized.Status,
		hasCursor,
		beforeAt,
		beforeID,
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
