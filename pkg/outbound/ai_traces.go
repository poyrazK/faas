package outbound

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// AI traces (ADR 20261020202035): the calls of one agent task, named by
// X-Gregale-AI-Trace or else by the trace ID of a W3C traceparent header.
// A traced call adds to its trace's row at settlement; trace limits refuse
// a trace's next call at admission.

// aiCallTrace reads an AI call's trace. ok is false for a malformed
// X-Gregale-AI-Trace; a malformed traceparent is not the gateway's to
// refuse, so it only names no trace.
func aiCallTrace(r *http.Request, integration Integration) (string, bool) {
	if integration.AIProvider == "" {
		return "", true
	}
	trace, ok := singleAIHeader(r, api.OutboundAITraceHeader)
	if !ok || trace != "" {
		return trace, ok
	}
	return traceparentTraceID(r.Header.Values("Traceparent")), true
}

// traceparentTraceID returns the trace ID of the one valid W3C traceparent
// header ("00-<32 hex>-<16 hex>-<2 hex>"), or "". Future versions may add
// fields after the four this version defines.
func traceparentTraceID(values []string) string {
	if len(values) != 1 {
		return ""
	}
	parts := strings.Split(strings.TrimSpace(values[0]), "-")
	if len(parts) < 4 || (parts[0] == "00" && len(parts) != 4) || parts[0] == "ff" {
		return ""
	}
	for index, size := range []int{2, 32, 16, 2} {
		if len(parts[index]) != size || !lowerHex(parts[index]) {
			return ""
		}
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return ""
	}
	return parts[1]
}

func lowerHex(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// aiTraceLimits are an integration's per-trace limits; zero is none.
type aiTraceLimits struct {
	calls, cost, repeats int64
}

func (l aiTraceLimits) any() bool { return l.calls > 0 || l.cost > 0 || l.repeats > 0 }

// traceLimits returns the integration's trace limits.
func (i Integration) traceLimits() aiTraceLimits {
	return aiTraceLimits{calls: positiveValue(i.AITraceMaxCalls), cost: positiveValue(i.AITraceMaxCostMillicents),
		repeats: positiveValue(i.AITraceMaxRepeatedToolCalls)}
}

func positiveValue(value *int64) int64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

// recordsAITraces reports an integration whose calls settle into their
// traces: one that keeps a request log or has trace limits.
func (i Integration) recordsAITraces() bool {
	return i.AIProvider != "" && (i.AIRequestLogEnabled || i.traceLimits().any())
}

// aiTraceState is one trace as admission reads it: whether it is recorded,
// its settled calls, cost and streak of identical tool requests, the new
// traces the integration recorded today, and its live leases.
type aiTraceState struct {
	known                       bool
	calls, cost, repeats        int64
	dayTraces                   int64
	inflightCalls, inflightCost int64
}

// aiTraceRejection returns the trace limit that refuses a call, or "". A
// trace never resets, so there is nothing to wait for.
func aiTraceRejection(state aiTraceState, limits aiTraceLimits, reserve aiReservation) string {
	switch {
	case !limits.any():
		return ""
	case !state.known && state.dayTraces >= api.OutboundAITracesPerDay:
		return ReasonTraceUntracked
	case limits.calls > 0 && state.calls+state.inflightCalls >= limits.calls:
		return ReasonTraceCallLimit
	case limits.cost > 0 && (state.cost >= limits.cost || state.cost+state.inflightCost+reserve.cost > limits.cost):
		return ReasonTraceCostLimit
	case limits.repeats > 0 && state.repeats >= limits.repeats:
		return ReasonTraceToolLoop
	}
	return ""
}

// traceRejection reports a refusal by a trace limit, which carries no
// Retry-After.
func traceRejection(reason string) bool {
	switch reason {
	case ReasonTraceCallLimit, ReasonTraceCostLimit, ReasonTraceToolLoop, ReasonTraceUntracked:
		return true
	}
	return false
}

// queueAITraceState reads a traced call's trace for its limits: the
// trace's row, the day's recorded traces and the trace's live leases. It
// joins the admission batch, so it adds no round trip.
func queueAITraceState(batch *pgx.Batch, integrationID uuid.UUID, today, now time.Time, traceID string) {
	batch.Queue(`
		SELECT trace.trace_id IS NOT NULL, COALESCE(trace.calls, 0), COALESCE(trace.cost_millicents, 0),
		       COALESCE(trace.repeated_tool_calls, 0),
		       COALESCE((SELECT days.traces FROM outbound_ai_trace_days days
		                  WHERE days.integration_id = $1 AND days.usage_date = $2::date), 0)::bigint,
		       held.calls, held.cost
		  FROM (SELECT 1) one
		  LEFT JOIN outbound_ai_traces trace ON trace.integration_id = $1 AND trace.trace_id = $4
		 CROSS JOIN LATERAL (
		       SELECT count(*)::bigint AS calls, COALESCE(sum(lease.ai_reserved_cost_millicents), 0)::bigint AS cost
		         FROM outbound_admission_leases lease
		        WHERE lease.integration_id = $1 AND lease.expires_at > $3 AND lease.ai_trace_id = $4) held`,
		integrationID, today, now, traceID)
}

func scanAITraceState(results pgx.BatchResults) (aiTraceState, error) {
	var state aiTraceState
	err := results.QueryRow().Scan(&state.known, &state.calls, &state.cost, &state.repeats, &state.dayTraces,
		&state.inflightCalls, &state.inflightCost)
	return state, err
}

// queueAITraceRollup adds a traced call to its trace's row. A new trace
// takes one of the day's OutboundAITracesPerDay slots; past them it is not
// recorded. A call whose response was read moves the trace's streak of
// identical tool requests: the same tools and arguments as the last call
// extend it, other tools start a new one, and no tools end it. Errors and
// unread responses leave it as it was.
func queueAITraceRollup(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord) {
	var errorCalls, unknownCalls int64
	switch record.Outcome {
	case AIUsageOutcomeError:
		errorCalls = 1
	case AIUsageOutcomeUnknown:
		unknownCalls = 1
	}
	streak := record.Outcome == AIUsageOutcomeKnown
	var signature []byte
	if streak {
		signature = nilIfEmptyBytes(record.ToolSignature)
	}
	batch.Queue(`
		WITH tracked AS (
		    SELECT EXISTS (SELECT 1 FROM outbound_ai_traces WHERE integration_id = $1 AND trace_id = $2) AS known
		), slot AS (
		    INSERT INTO outbound_ai_trace_days AS days (integration_id, usage_date, traces)
		    SELECT $1, (now() AT TIME ZONE 'UTC')::date, 1 FROM tracked WHERE NOT tracked.known
		    ON CONFLICT (integration_id, usage_date) DO UPDATE SET traces = days.traces + 1 WHERE days.traces < $3
		    RETURNING 1
		)
		INSERT INTO outbound_ai_traces AS trace
		    (integration_id, trace_id, caller, end_user, calls, error_calls, usage_unknown_calls, input_tokens, output_tokens,
		     cost_millicents, tool_calls, tool_signature, repeated_tool_calls, models)
		SELECT $1, $2, $4, NULLIF($5, ''), 1, $6, $7, $8, $9, $10, $11, $12::bytea,
		       CASE WHEN $12::bytea IS NULL THEN 0 ELSE 1 END, ARRAY[$13::text]
		  FROM tracked
		 WHERE tracked.known OR EXISTS (SELECT 1 FROM slot)
		ON CONFLICT (integration_id, trace_id) DO UPDATE SET
		    last_at = greatest(trace.last_at, now()),
		    calls = trace.calls + 1,
		    error_calls = trace.error_calls + EXCLUDED.error_calls,
		    usage_unknown_calls = trace.usage_unknown_calls + EXCLUDED.usage_unknown_calls,
		    input_tokens = trace.input_tokens + EXCLUDED.input_tokens,
		    output_tokens = trace.output_tokens + EXCLUDED.output_tokens,
		    cost_millicents = trace.cost_millicents + EXCLUDED.cost_millicents,
		    tool_calls = trace.tool_calls + EXCLUDED.tool_calls,
		    tool_signature = CASE WHEN $14 THEN EXCLUDED.tool_signature ELSE trace.tool_signature END,
		    repeated_tool_calls = CASE
		        WHEN NOT $14 THEN trace.repeated_tool_calls
		        WHEN EXCLUDED.tool_signature IS NULL THEN 0
		        WHEN trace.tool_signature = EXCLUDED.tool_signature THEN trace.repeated_tool_calls + 1
		        ELSE 1 END,
		    models = CASE WHEN $13 = ANY(trace.models) OR cardinality(trace.models) >= $15 THEN trace.models
		                  ELSE trace.models || $13::text END`,
		integrationID, record.TraceID, api.OutboundAITracesPerDay, record.Caller, record.EndUser, errorCalls, unknownCalls,
		record.Usage.InputTokens, record.Usage.OutputTokens, record.CostMillicents, int64(len(record.ToolCalls)), signature,
		record.Usage.Model, streak, api.OutboundAITraceModelsMax)
}

func nilIfEmptyBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}

// memoryTrace is one trace in the memory backend.
type memoryTrace struct {
	trace     AITrace
	signature []byte
}

// AITrace is one recorded trace, as tests read it from the memory backend.
type AITrace struct {
	TraceID, Caller, EndUser                  string
	Calls, ErrorCalls, UsageUnknownCalls      int64
	InputTokens, OutputTokens, CostMillicents int64
	ToolCalls, RepeatedToolCalls              int64
	Models                                    []string
}

// add settles one call into the trace, as queueAITraceRollup does.
func (t *memoryTrace) add(record AIUsageRecord) {
	trace := &t.trace
	trace.Calls++
	switch record.Outcome {
	case AIUsageOutcomeError:
		trace.ErrorCalls++
	case AIUsageOutcomeUnknown:
		trace.UsageUnknownCalls++
	}
	trace.InputTokens += record.Usage.InputTokens
	trace.OutputTokens += record.Usage.OutputTokens
	trace.CostMillicents += record.CostMillicents
	trace.ToolCalls += int64(len(record.ToolCalls))
	if record.Outcome == AIUsageOutcomeKnown {
		signature := nilIfEmptyBytes(record.ToolSignature)
		switch {
		case signature == nil:
			trace.RepeatedToolCalls = 0
		case t.signature != nil && string(t.signature) == string(signature):
			trace.RepeatedToolCalls++
		default:
			trace.RepeatedToolCalls = 1
		}
		t.signature = append([]byte(nil), signature...)
	}
	if !slices.Contains(trace.Models, record.Usage.Model) && len(trace.Models) < api.OutboundAITraceModelsMax {
		trace.Models = append(trace.Models, record.Usage.Model)
	}
}

// validateAITraceRecord checks a settlement's trace and requested tools.
func validateAITraceRecord(record AIUsageRecord) error {
	if record.TraceID != "" && !api.ValidOutboundAITraceID(record.TraceID) {
		return fmt.Errorf("%w: AI usage trace is invalid", ErrInvalidIntegration)
	}
	if len(record.ToolCalls) > api.OutboundAIToolCallsPerResponseMax || (len(record.ToolSignature) != 0 && len(record.ToolSignature) != 16) {
		return fmt.Errorf("%w: AI usage tool calls are invalid", ErrInvalidIntegration)
	}
	for _, name := range record.ToolCalls {
		if name == "" || sanitizeAIToolName(name) != name {
			return fmt.Errorf("%w: AI usage tool name is invalid", ErrInvalidIntegration)
		}
	}
	return nil
}

// tracedForLimits is the trace admission reads: the call's, when the
// integration limits traces.
func tracedForLimits(traceID string, limits aiTraceLimits) string {
	if !limits.any() {
		return ""
	}
	return traceID
}
