package outboundai

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
)

const guardrailsInvalidDetail = "requests_per_minute must be null or 1 to 100,000; tokens_per_minute null or 1 to 1,000,000,000; " +
	"max_output_tokens null or 1 to 1,000,000; allowed_models null or 1 to 32 distinct model names or prefixes ending in *"

func (s *Service) controlStore(w http.ResponseWriter) (state.OutboundAIControlStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIControlStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
	}
	return store, ok
}

// PutBindingGuardrails replaces one app's per-minute limits, model
// allow-list and output ceiling on an AI integration it is bound to (ADR-991).
func (s *Service) PutBindingGuardrails(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundBindingAIGuardrailsRequest
	if err := httpkit.DecodeJSONSized(r, &req, 8<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, guardrailsInvalidDetail)
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundBindingAIGuardrails(r.Context(), acct.ID, app.ID, integrationID, state.OutboundAIGuardrails{
		RequestsPerMinute: req.RequestsPerMinute, TokensPerMinute: req.TokensPerMinute,
		AllowedModels: req.AllowedModels, MaxOutputTokens: req.MaxOutputTokens,
	})
	s.writeResult(w, err, "AI outbound binding not found", guardrailsInvalidDetail)
}

// ListCredentialKeys lists an integration's extra keys; values are
// write-only.
func (s *Service) ListCredentialKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	keys, err := store.ListOutboundCredentialKeys(r.Context(), acct.ID, integrationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential keys are unavailable")
	default:
		out := api.OutboundCredentialKeyList{Items: make([]api.OutboundCredentialKey, 0, len(keys))}
		for _, key := range keys {
			out.Items = append(out.Items, api.OutboundCredentialKey{ID: key.ID, Label: key.Label, CreatedAt: key.CreatedAt})
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// CreateCredentialKey seals and stores an extra provider key for a
// customer-sealed integration (ADR-991).
func (s *Service) CreateCredentialKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.CreateOutboundCredentialKeyRequest
	if err := httpkit.DecodeJSONSized(r, &req, 10<<10); err != nil || !api.ValidOutboundCredentialKeyLabel(req.Label) ||
		!outbound.ValidManagedAuthorization(req.Authorization) {
		problem(w, http.StatusBadRequest, api.CodeValidation, "A DNS-label key label and a valid Authorization value are required")
		return
	}
	sealed, ok := s.Host.SealCredential(w, req.Authorization)
	if !ok {
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	key, err := store.CreateOutboundCredentialKey(r.Context(), state.OutboundCredentialKey{
		ID: uuid.NewString(), AccountID: acct.ID, IntegrationID: integrationID, Label: req.Label, AuthorizationSealed: sealed,
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "customer-sealed outbound integration not found")
	case errors.Is(err, state.ErrConflict):
		problem(w, http.StatusConflict, api.CodeConflict, "A key with this label already exists")
	case errors.Is(err, state.ErrOutboundCredentialKeyLimit):
		problem(w, http.StatusTooManyRequests, "outbound_credential_key_limit", "The integration has reached its extra key limit of 8")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential key could not be saved")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusCreated, api.OutboundCredentialKey{ID: key.ID, Label: key.Label, CreatedAt: key.CreatedAt})
	}
}

func (s *Service) DeleteCredentialKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Key ID must be a UUID")
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundCredentialKey(r.Context(), acct.ID, integrationID, keyID)
	s.writeResult(w, err, "outbound credential key not found", "")
}

// ListBudgetAlerts lists the budget thresholds outboundd recorded,
// newest first (ADR-991).
func (s *Service) ListBudgetAlerts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, api.OutboundAIBudgetAlertsPageMax)
	if !ok {
		problem(w, http.StatusBadRequest, api.CodeValidation, "limit must be 1 to 100")
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	alerts, err := store.ListOutboundAIBudgetAlerts(r.Context(), acct.ID, integrationID, limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI budget alerts are unavailable")
	default:
		out := api.OutboundAIBudgetAlertList{Items: make([]api.OutboundAIBudgetAlert, 0, len(alerts))}
		for _, alert := range alerts {
			item := api.OutboundAIBudgetAlert{ID: alert.ID, IntegrationID: alert.IntegrationID, Budget: alert.Budget,
				PeriodStart: alert.PeriodStart.Format("2006-01-02"), ThresholdPercent: alert.ThresholdPercent,
				Limit: alert.Limit, Used: alert.Used, CrossedAt: alert.CrossedAt}
			if alert.AppID != "" {
				appID := alert.AppID
				item.AppID = &appID
			}
			if alert.EndUser != "" {
				endUser := alert.EndUser
				item.EndUser = &endUser
			}
			if alert.Tag != "" {
				tag := alert.Tag
				item.Tag = &tag
			}
			out.Items = append(out.Items, item)
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// ListRequestLog pages the opt-in request log newest first;
// ?before= takes the previous page's next_before (ADR-991).
func (s *Service) ListRequestLog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, limitOK := queryLimit(r, api.OutboundAIRequestLogPageMax)
	var before *time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			limitOK = false
		}
		before = &parsed
	}
	if !limitOK {
		problem(w, http.StatusBadRequest, api.CodeValidation, "limit must be 1 to 200 and before an RFC 3339 time")
		return
	}
	store, ok := s.controlStore(w)
	if !ok {
		return
	}
	entries, err := store.ListOutboundAIRequestLog(r.Context(), acct.ID, integrationID, before, limit)
	var visible map[string]bool
	if err == nil {
		callers := make([]string, 0, len(entries))
		for _, entry := range entries {
			callers = append(callers, entry.Caller)
		}
		visible, err = s.visibleCallers(r, acct, callers)
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The outbound AI request log is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, requestLogResponse(entries, visible, limit))
	}
}

// requestLogResponse lists the entries whose caller is visible; the cursor
// follows the whole page, so a withheld call never ends the listing early.
func requestLogResponse(entries []state.OutboundAIRequestLogEntry, visible map[string]bool, limit int) api.OutboundAIRequestLogList {
	out := api.OutboundAIRequestLogList{Items: make([]api.OutboundAIRequestLogEntry, 0, len(entries))}
	for _, entry := range entries {
		if !visible[entry.Caller] {
			continue
		}
		item := api.OutboundAIRequestLogEntry{
			ID: entry.ID, CreatedAt: entry.CreatedAt, Caller: entry.Caller, Model: entry.Model, Status: entry.Status,
			Outcome: entry.Outcome, InputTokens: entry.InputTokens, CachedInputTokens: entry.CachedInputTokens,
			CacheWriteTokens: entry.CacheWriteTokens, OutputTokens: entry.OutputTokens, CostMillicents: entry.CostMillicents,
			DurationMS: entry.DurationMS, TimeToFirstByteMS: copyInt64(entry.TTFBMS), Attempts: entry.Attempts,
			ContentCaptured: entry.ContentCaptured, Tags: append([]string{}, entry.Tags...),
		}
		if entry.TraceID != "" {
			traceID := entry.TraceID
			item.TraceID = &traceID
		}
		if len(entry.ToolCalls) > 0 {
			item.ToolCalls = append([]string{}, entry.ToolCalls...)
		}
		qualityFields(&item, entry)
		if entry.RouteID != "" {
			routeID := entry.RouteID
			item.RouteID = &routeID
		}
		if entry.EndUser != "" {
			endUser := entry.EndUser
			item.EndUser = &endUser
		}
		out.Items = append(out.Items, item)
	}
	if len(entries) == limit && len(entries) > 0 {
		next := entries[len(entries)-1].CreatedAt
		out.NextBefore = &next
	}
	return out
}
