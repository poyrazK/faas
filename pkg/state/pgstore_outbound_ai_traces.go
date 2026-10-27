package state

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAITraceStore = (*PgStore)(nil)

const outboundAITraceColumns = `trace_id, caller, COALESCE(end_user, ''), first_at, last_at, calls, error_calls,
	       usage_unknown_calls, input_tokens, output_tokens, cost_millicents, tool_calls, repeated_tool_calls, models`

func scanOutboundAITrace(row pgx.Row) (OutboundAITrace, error) {
	var trace OutboundAITrace
	err := row.Scan(&trace.TraceID, &trace.Caller, &trace.EndUser, &trace.FirstAt, &trace.LastAt, &trace.Calls, &trace.ErrorCalls,
		&trace.UsageUnknownCalls, &trace.InputTokens, &trace.OutputTokens, &trace.CostMillicents, &trace.ToolCalls,
		&trace.RepeatedToolCalls, &trace.Models)
	trace.FirstAt, trace.LastAt = trace.FirstAt.UTC(), trace.LastAt.UTC()
	return trace, err
}

// ListOutboundAITraces lists traces by last call, newest first, before an
// optional last-call cursor.
func (s *PgStore) ListOutboundAITraces(ctx context.Context, accountID, integrationID string, query OutboundAITraceQuery) ([]OutboundAITrace, error) {
	if !query.valid() {
		return nil, ErrInvalidArgument
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+outboundAITraceColumns+`
		  FROM outbound_ai_traces
		 WHERE integration_id = $1 AND ($2::timestamptz IS NULL OR last_at < $2)
		   AND ($3 = '' OR caller = $3) AND ($4 = '' OR end_user = $4)
		 ORDER BY last_at DESC, trace_id
		 LIMIT $5`, mustPgUUID(integrationID), query.Before, query.AppID, query.EndUser,
		boundedPage(query.Limit, api.OutboundAITracePageMax))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	traces := make([]OutboundAITrace, 0)
	for rows.Next() {
		trace, err := scanOutboundAITrace(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		traces = append(traces, trace)
	}
	return traces, mapErr(rows.Err())
}

// GetOutboundAITrace reads one trace and its first OutboundAITraceCallsMax
// logged calls, oldest first.
func (s *PgStore) GetOutboundAITrace(ctx context.Context, accountID, integrationID, traceID string) (OutboundAITraceDetail, error) {
	if !api.ValidOutboundAITraceID(traceID) {
		return OutboundAITraceDetail{}, ErrNotFound
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return OutboundAITraceDetail{}, err
	}
	trace, err := scanOutboundAITrace(s.pool.QueryRow(ctx, `
		SELECT `+outboundAITraceColumns+`
		  FROM outbound_ai_traces
		 WHERE integration_id = $1 AND trace_id = $2`, mustPgUUID(integrationID), traceID))
	if err != nil {
		return OutboundAITraceDetail{}, mapErr(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+outboundAIRequestLogColumns+`
		  FROM outbound_ai_request_log log
		 WHERE log.integration_id = $1 AND log.trace_id = $2
		 ORDER BY log.created_at, log.id
		 LIMIT $3`, mustPgUUID(integrationID), traceID, api.OutboundAITraceCallsMax+1)
	if err != nil {
		return OutboundAITraceDetail{}, mapErr(err)
	}
	calls, err := scanOutboundAIRequestLogRows(rows)
	if err != nil {
		return OutboundAITraceDetail{}, err
	}
	detail := OutboundAITraceDetail{Trace: trace, Calls: calls}
	if len(calls) > api.OutboundAITraceCallsMax {
		detail.Calls, detail.CallsTruncated = calls[:api.OutboundAITraceCallsMax], true
	}
	return detail, nil
}
