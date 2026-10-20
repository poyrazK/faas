package outboundai

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// PolicyInvalidDetail explains a rejected AI policy; integration creation
// in apidserver shares it.
const PolicyInvalidDetail = "ai_provider must be openai, anthropic, gemini, azure_openai, bedrock or vertex; token limits (1 to 100,000,000,000), cost limits (1 to 1,000,000,000,000 millicents) and the request log require a provider; request_log_content requires the request log; log_export and allow_unmetered require a provider"

func (s *Service) aiStore(w http.ResponseWriter) (state.OutboundAIStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
	}
	return store, ok
}

// writeResult maps a store write to the shared response set.
func (s *Service) writeResult(w http.ResponseWriter, err error, notFound, invalid string) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, notFound)
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, invalid)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings could not be updated")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

// PutPolicy replaces a customer integration's AI gateway settings
// (ADR-988, ADR-990). An empty provider turns AI handling off.
func (s *Service) PutPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIPolicyRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, PolicyInvalidDetail)
		return
	}
	store, ok := s.aiStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIPolicy(r.Context(), acct.ID, integrationID, state.OutboundAIPolicy{
		Provider: req.AIProvider, DailyTokenLimit: req.DailyTokenLimit,
		DailyCostLimit: req.DailyCostLimitMillicents, MonthlyCostLimit: req.MonthlyCostLimitMillicents,
		RequestLogEnabled: req.RequestLogEnabled, UserDailyTokenLimit: req.UserDailyTokenLimit,
		UserDailyCostLimit: req.UserDailyCostLimitMillicents, RequestLogContent: req.RequestLogContent,
		LogExport: req.LogExport, AllowUnmetered: req.AllowUnmetered, AutoPromptCache: req.AutoPromptCache,
	})
	s.writeResult(w, err, "customer outbound integration not found", PolicyInvalidDetail)
}

// GetUsage reports one UTC day of AI usage by caller and model.
// ?date=YYYY-MM-DD selects a day inside the retention window.
func (s *Service) GetUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.aiStore(w)
	if !ok {
		return
	}
	usage, err := store.GetOutboundAIUsage(r.Context(), acct.ID, integrationID, r.URL.Query().Get("date"))
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, "date must be a YYYY-MM-DD UTC day within the last 90 days")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound AI usage is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, usageResponse(usage))
	}
}

func usageResponse(usage state.OutboundAIUsage) api.OutboundAIUsageResponse {
	out := api.OutboundAIUsageResponse{
		UsageDate: usage.UsageDate, AIProvider: usage.AIProvider, DailyTokenCount: usage.DailyTokenCount,
		DailyTokenLimit: copyInt64(usage.DailyTokenLimit), ResetsAt: usage.ResetsAt,
		Items:               make([]api.OutboundAIUsageItem, 0, len(usage.Items)),
		DailyCostMillicents: usage.DailyCostMillicents, DailyCostLimitMillicents: copyInt64(usage.DailyCostLimit),
		Month: usage.Month, MonthlyCostMillicents: usage.MonthlyCostMillicents,
		MonthlyCostLimitMillicents: copyInt64(usage.MonthlyCostLimit),
	}
	for _, item := range usage.Items {
		out.Items = append(out.Items, api.OutboundAIUsageItem{
			Caller: item.Caller, Model: item.Model, Requests: item.Requests, ErrorRequests: item.ErrorRequests,
			UsageUnknownRequests: item.UsageUnknownRequests, InputTokens: item.InputTokens,
			CachedInputTokens: item.CachedInputTokens, CacheWriteTokens: item.CacheWriteTokens, OutputTokens: item.OutputTokens,
			CostMillicents: item.CostMillicents, Images: item.Images, AudioSeconds: item.AudioSeconds, Characters: item.Characters,
			VideoSeconds: item.VideoSeconds, WebSearches: item.WebSearches, FileSearches: item.FileSearches,
			CodeSessionGB: item.CodeSessionGB, CachedRequests: item.CachedRequests, CacheSavedMillicents: item.CacheSavedMillicents,
			PromptCacheSavedMillicents: item.PromptCacheSavedMillicents,
			InputAudioTokens:           item.InputAudioTokens, OutputAudioTokens: item.OutputAudioTokens,
		})
	}
	return out
}

// PutBindingBudget sets or clears one app's daily AI token limit
// on an AI integration it is bound to (ADR-990).
func (s *Service) PutBindingBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundBindingAIBudgetRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil ||
		(req.DailyTokenLimit != nil && (*req.DailyTokenLimit < 1 || *req.DailyTokenLimit > api.MaxOutboundAITokensPerDay)) {
		problem(w, http.StatusBadRequest, api.CodeValidation, "daily_token_limit must be null or 1 to 100,000,000,000")
		return
	}
	store, ok := s.aiStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundBindingAITokenLimit(r.Context(), acct.ID, app.ID, integrationID, req.DailyTokenLimit)
	s.writeResult(w, err, "AI outbound binding not found", "daily_token_limit must be null or 1 to 100,000,000,000")
}

// GetPrices returns the price list currently in effect.
func (s *Service) GetPrices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.aiStore(w)
	if !ok {
		return
	}
	list, err := store.GetOutboundAIPrices(r.Context(), acct.ID, integrationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI prices are unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.OutboundAIPriceListResponse{EffectiveFrom: list.EffectiveFrom, Entries: list.Entries})
	}
}

// PutPrices replaces the price list; it takes effect immediately
// and earlier lists stay for the costs already settled (ADR-990).
func (s *Service) PutPrices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	const invalid = "entries need unique printable model prefixes (or \"*\") and prices from 0 to 1,000,000,000 millicents per million tokens; at most 64 entries"
	var req api.PutOutboundAIPricesRequest
	if err := httpkit.DecodeJSONSized(r, &req, 32<<10); err != nil || !api.ValidOutboundAIPriceEntries(req.Entries) {
		problem(w, http.StatusBadRequest, api.CodeValidation, invalid)
		return
	}
	store, ok := s.aiStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundAIPrices(r.Context(), acct.ID, integrationID, req.Entries)
	s.writeResult(w, err, "customer outbound integration not found", invalid)
}

// contentPolicyInvalidDetail explains a rejected content policy (ADR-997).
const contentPolicyInvalidDetail = "email, payment_card, iban and secret must be off, redact or block; blocked_terms at most 32 " +
	"distinct printable terms of 1 to 128 bytes; an active policy requires an AI provider; moderation.action must be off, flag or block, " +
	"and an active check needs kind openai_moderation (an OpenAI integration, without prompt_injection) or classifier (with a model), " +
	"integration_id naming one of this account's enabled AI integrations, distinct categories from prompt_injection, harassment, hate, " +
	"self_harm, sexual, violence and illicit, and on_error allow or block; response.email, payment_card, iban and secret must be off, " +
	"redact or block, response.blocked_terms off or block, and response.moderation off, flag or block, which needs a complete " +
	"moderation check with a category besides prompt_injection"

// PutContentPolicy replaces a customer AI integration's content guardrails
// (ADR-997).
func (s *Service) PutContentPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var policy api.OutboundAIContentPolicy
	if err := httpkit.DecodeJSONSized(r, &policy, 16<<10); err != nil || !policy.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, contentPolicyInvalidDetail)
		return
	}
	store, ok := s.Host.Store().(state.OutboundAIContentPolicyStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
		return
	}
	err := store.SetOutboundAIContentPolicy(r.Context(), acct.ID, integrationID, policy)
	s.writeResult(w, err, "customer outbound integration not found", contentPolicyInvalidDetail)
}
