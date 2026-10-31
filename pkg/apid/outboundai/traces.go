package outboundai

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const traceListInvalidDetail = "limit must be 1 to 200, before an RFC 3339 time, app_id a UUID and end_user an ID of 1 to 64 " +
	"letters, digits or . _ : @ + = / | -"

func (s *Service) traceStore(w http.ResponseWriter) (state.OutboundAITraceStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAITraceStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI traces are unavailable")
	}
	return store, ok
}

// ListTraces lists an integration's AI traces, newest first
// (ADR 20261020202035).
func (s *Service) ListTraces(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, valid := queryLimit(r, api.OutboundAITracePageMax)
	values := r.URL.Query()
	query := state.OutboundAITraceQuery{AppID: values.Get("app_id"), EndUser: values.Get("end_user"), Limit: limit}
	if raw := values.Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		valid = valid && err == nil
		query.Before = &parsed
	}
	if !valid {
		problem(w, http.StatusBadRequest, api.CodeValidation, traceListInvalidDetail)
		return
	}
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	traces, err := store.ListOutboundAITraces(r.Context(), acct.ID, integrationID, query)
	var visible map[string]bool
	if err == nil {
		callers := make([]string, 0, len(traces))
		for _, trace := range traces {
			callers = append(callers, trace.Caller)
		}
		visible, err = s.visibleCallers(r, acct, callers)
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, traceListInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI traces are unavailable")
	default:
		out := api.OutboundAITraceList{Items: make([]api.OutboundAITrace, 0, len(traces))}
		for _, trace := range traces {
			if visible[trace.Caller] {
				out.Items = append(out.Items, traceResponse(trace))
			}
		}
		if len(traces) == limit && len(traces) > 0 {
			next := traces[len(traces)-1].LastAt
			out.NextBefore = &next
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// GetTrace reads one AI trace with its logged calls, oldest first
// (ADR 20261020202035).
func (s *Service) GetTrace(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	traceID := r.PathValue("trace")
	if !api.ValidOutboundAITraceID(traceID) {
		httpkit.NotFound(w, "AI trace not found")
		return
	}
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	detail, err := store.GetOutboundAITrace(r.Context(), acct.ID, integrationID, traceID)
	var visible map[string]bool
	if err == nil {
		callers := []string{detail.Trace.Caller}
		for _, entry := range detail.Calls {
			callers = append(callers, entry.Caller)
		}
		visible, err = s.visibleCallers(r, acct, callers)
	}
	switch {
	case errors.Is(err, state.ErrNotFound), err == nil && !visible[detail.Trace.Caller]:
		httpkit.NotFound(w, "AI trace not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI traces are unavailable")
	default:
		out := api.OutboundAITraceResponse{Trace: traceResponse(detail.Trace), CallsTruncated: detail.CallsTruncated,
			Calls: make([]api.OutboundAITraceCall, 0, len(detail.Calls))}
		for _, entry := range detail.Calls {
			if !visible[entry.Caller] {
				continue
			}
			out.Calls = append(out.Calls, api.OutboundAITraceCall{
				ID: entry.ID, CreatedAt: entry.CreatedAt, Caller: entry.Caller, Model: entry.Model, Status: entry.Status,
				Outcome: entry.Outcome, InputTokens: entry.InputTokens, OutputTokens: entry.OutputTokens,
				CostMillicents: entry.CostMillicents, DurationMS: entry.DurationMS, TimeToFirstByteMS: copyInt64(entry.TTFBMS),
				ToolCalls: append([]string{}, entry.ToolCalls...),
			})
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func traceResponse(trace state.OutboundAITrace) api.OutboundAITrace {
	out := api.OutboundAITrace{
		TraceID: trace.TraceID, FirstAt: trace.FirstAt, LastAt: trace.LastAt, Calls: trace.Calls, ErrorCalls: trace.ErrorCalls,
		UsageUnknownCalls: trace.UsageUnknownCalls, InputTokens: trace.InputTokens, OutputTokens: trace.OutputTokens,
		CostMillicents: trace.CostMillicents, ToolCalls: trace.ToolCalls, RepeatedToolCalls: trace.RepeatedToolCalls,
		Models: append([]string{}, trace.Models...),
	}
	if _, err := uuid.Parse(trace.Caller); err == nil {
		appID := trace.Caller
		out.AppID = &appID
	}
	if trace.EndUser != "" {
		endUser := trace.EndUser
		out.EndUser = &endUser
	}
	return out
}
