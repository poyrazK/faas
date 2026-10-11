package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const outboundAIPolicyInvalidDetail = "ai_provider must be openai, anthropic, gemini, azure_openai, bedrock or vertex; token limits (1 to 100,000,000,000), cost limits (1 to 1,000,000,000,000 millicents) and the request log require a provider"

func (s *server) outboundAIStore(w http.ResponseWriter) (state.OutboundAIStore, bool) {
	store, ok := s.store.(state.OutboundAIStore)
	if !ok {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
	}
	return store, ok
}

func outboundIntegrationPathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return "", false
	}
	return integrationID, true
}

// writeOutboundAIWriteResult maps a store write to the shared response set.
func (s *server) writeOutboundAIWriteResult(w http.ResponseWriter, err error, notFound, invalid string) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, notFound)
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, invalid)
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings could not be updated")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

// putOutboundAIPolicy replaces a customer integration's AI gateway settings
// (ADR-979, ADR-983). An empty provider turns AI handling off.
func (s *server) putOutboundAIPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIPolicyRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIPolicyInvalidDetail)
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIPolicy(r.Context(), acct.ID, integrationID, state.OutboundAIPolicy{
		Provider: req.AIProvider, DailyTokenLimit: req.DailyTokenLimit,
		DailyCostLimit: req.DailyCostLimitMillicents, MonthlyCostLimit: req.MonthlyCostLimitMillicents,
		RequestLogEnabled: req.RequestLogEnabled,
	})
	s.writeOutboundAIWriteResult(w, err, "customer outbound integration not found", outboundAIPolicyInvalidDetail)
}

// getOutboundAIUsage reports one UTC day of AI usage by caller and model.
// ?date=YYYY-MM-DD selects a day inside the retention window.
func (s *server) getOutboundAIUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
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
		Items:               make([]api.OutboundAIUsageItem, 0, len(usage.Items)),
		DailyCostMillicents: usage.DailyCostMillicents, DailyCostLimitMillicents: copyOutboundDailyLimit(usage.DailyCostLimit),
		Month: usage.Month, MonthlyCostMillicents: usage.MonthlyCostMillicents,
		MonthlyCostLimitMillicents: copyOutboundDailyLimit(usage.MonthlyCostLimit),
	}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAIUsageItem{
			Caller: item.Caller, Model: item.Model, Requests: item.Requests, ErrorRequests: item.ErrorRequests,
			UsageUnknownRequests: item.UsageUnknownRequests, InputTokens: item.InputTokens,
			CachedInputTokens: item.CachedInputTokens, CacheWriteTokens: item.CacheWriteTokens, OutputTokens: item.OutputTokens,
			CostMillicents: item.CostMillicents,
		})
	}
	return out
}

// putOutboundBindingAIBudget sets or clears one app's daily AI token limit
// on an AI integration it is bound to (ADR-983).
func (s *server) putOutboundBindingAIBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundBindingAIBudgetRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil ||
		(req.DailyTokenLimit != nil && (*req.DailyTokenLimit < 1 || *req.DailyTokenLimit > api.MaxOutboundAITokensPerDay)) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "daily_token_limit must be null or 1 to 100,000,000,000")
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundBindingAITokenLimit(r.Context(), acct.ID, app.ID, integrationID, req.DailyTokenLimit)
	s.writeOutboundAIWriteResult(w, err, "AI outbound binding not found", "daily_token_limit must be null or 1 to 100,000,000,000")
}

// getOutboundAIPrices returns the price list currently in effect.
func (s *server) getOutboundAIPrices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	list, err := store.GetOutboundAIPrices(r.Context(), acct.ID, integrationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound integration not found")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI prices are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, api.OutboundAIPriceListResponse{EffectiveFrom: list.EffectiveFrom, Entries: list.Entries})
	}
}

// putOutboundAIPrices replaces the price list; it takes effect immediately
// and earlier lists stay for the costs already settled (ADR-983).
func (s *server) putOutboundAIPrices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	const invalid = "entries need unique printable model prefixes (or \"*\") and prices from 0 to 1,000,000,000 millicents per million tokens; at most 64 entries"
	var req api.PutOutboundAIPricesRequest
	if err := decodeJSONSized(r, &req, 32<<10); err != nil || !api.ValidOutboundAIPriceEntries(req.Entries) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, invalid)
		return
	}
	store, ok := s.outboundAIStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIPrices(r.Context(), acct.ID, integrationID, req.Entries)
	s.writeOutboundAIWriteResult(w, err, "customer outbound integration not found", invalid)
}
