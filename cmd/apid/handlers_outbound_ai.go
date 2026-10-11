package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const outboundAIPolicyInvalidDetail = "ai_provider must be openai or anthropic, and a daily token limit (1 to 100,000,000,000) requires a provider"

func (s *server) outboundAIStore(w http.ResponseWriter) (state.OutboundAIStore, bool) {
	store, ok := s.store.(state.OutboundAIStore)
	if !ok {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
	}
	return store, ok
}

// putOutboundAIPolicy replaces a customer integration's AI gateway settings
// (ADR-979). An empty provider turns AI handling off.
func (s *server) putOutboundAIPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	var req api.PutOutboundAIPolicyRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil || !api.ValidOutboundAIPolicy(req.AIProvider, req.DailyTokenLimit) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIPolicyInvalidDetail)
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIPolicy(r.Context(), acct.ID, integrationID, req.AIProvider, req.DailyTokenLimit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "customer outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIPolicyInvalidDetail)
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings could not be updated")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

// getOutboundAIUsage reports one UTC day of AI usage by caller and model.
// ?date=YYYY-MM-DD selects a day inside the retention window.
func (s *server) getOutboundAIUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	usage, err := store.GetOutboundAIUsage(r.Context(), acct.ID, integrationID, r.URL.Query().Get("date"))
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "date must be a YYYY-MM-DD UTC day within the last 90 days")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, outboundAIUsageResponse(usage))
	}
}

func outboundAIUsageResponse(usage state.OutboundAIUsage) api.OutboundAIUsageResponse {
	out := api.OutboundAIUsageResponse{
		UsageDate: usage.UsageDate, AIProvider: usage.AIProvider, DailyTokenCount: usage.DailyTokenCount,
		DailyTokenLimit: copyOutboundDailyLimit(usage.DailyTokenLimit), ResetsAt: usage.ResetsAt,
		Items: make([]api.OutboundAIUsageItem, 0, len(usage.Items)),
	}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAIUsageItem{
			Caller: item.Caller, Model: item.Model, Requests: item.Requests, ErrorRequests: item.ErrorRequests,
			UsageUnknownRequests: item.UsageUnknownRequests, InputTokens: item.InputTokens,
			CachedInputTokens: item.CachedInputTokens, CacheWriteTokens: item.CacheWriteTokens, OutputTokens: item.OutputTokens,
		})
	}
	return out
}
