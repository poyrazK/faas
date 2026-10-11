package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const outboundAIGuardrailsInvalidDetail = "requests_per_minute must be null or 1 to 100,000; tokens_per_minute null or 1 to 1,000,000,000; " +
	"max_output_tokens null or 1 to 1,000,000; allowed_models null or 1 to 32 distinct model names or prefixes ending in *"

func (s *server) outboundAIControlStore(w http.ResponseWriter) (state.OutboundAIControlStore, bool) {
	store, ok := s.store.(state.OutboundAIControlStore)
	if !ok {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI gateway settings are unavailable")
	}
	return store, ok
}

// putOutboundBindingAIGuardrails replaces one app's per-minute limits, model
// allow-list and output ceiling on an AI integration it is bound to (ADR-984).
func (s *server) putOutboundBindingAIGuardrails(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundBindingAIGuardrailsRequest
	if err := decodeJSONSized(r, &req, 8<<10); err != nil || !req.Valid() {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, outboundAIGuardrailsInvalidDetail)
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundBindingAIGuardrails(r.Context(), acct.ID, app.ID, integrationID, state.OutboundAIGuardrails{
		RequestsPerMinute: req.RequestsPerMinute, TokensPerMinute: req.TokensPerMinute,
		AllowedModels: req.AllowedModels, MaxOutputTokens: req.MaxOutputTokens,
	})
	s.writeOutboundAIWriteResult(w, err, "AI outbound binding not found", outboundAIGuardrailsInvalidDetail)
}

// listOutboundCredentialKeys lists an integration's extra keys; values are
// write-only.
func (s *server) listOutboundCredentialKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	keys, err := store.ListOutboundCredentialKeys(r.Context(), acct.ID, integrationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound integration not found")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential keys are unavailable")
	default:
		out := api.OutboundCredentialKeyList{Items: make([]api.OutboundCredentialKey, 0, len(keys))}
		for _, key := range keys {
			out.Items = append(out.Items, api.OutboundCredentialKey{ID: key.ID, Label: key.Label, CreatedAt: key.CreatedAt})
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}
}

// createOutboundCredentialKey seals and stores an extra provider key for a
// customer-sealed integration (ADR-984).
func (s *server) createOutboundCredentialKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	var req api.CreateOutboundCredentialKeyRequest
	if err := decodeJSONSized(r, &req, 10<<10); err != nil || !api.ValidOutboundCredentialKeyLabel(req.Label) ||
		!outbound.ValidManagedAuthorization(req.Authorization) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "A DNS-label key label and a valid Authorization value are required")
		return
	}
	sealed, ok := sealOutboundCredential(w, req.Authorization)
	if !ok {
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	key, err := store.CreateOutboundCredentialKey(r.Context(), state.OutboundCredentialKey{
		ID: uuid.NewString(), AccountID: acct.ID, IntegrationID: integrationID, Label: req.Label, AuthorizationSealed: sealed,
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "customer-sealed outbound integration not found")
	case errors.Is(err, state.ErrConflict):
		outboundBindingProblem(w, http.StatusConflict, api.CodeConflict, "A key with this label already exists")
	case errors.Is(err, state.ErrOutboundCredentialKeyLimit):
		outboundBindingProblem(w, http.StatusTooManyRequests, "outbound_credential_key_limit", "The integration has reached its extra key limit of 8")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential key could not be saved")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, api.OutboundCredentialKey{ID: key.ID, Label: key.Label, CreatedAt: key.CreatedAt})
	}
}

// sealOutboundCredential seals a provider credential for outboundd only.
func sealOutboundCredential(w http.ResponseWriter, authorization string) ([]byte, bool) {
	unavailable := func() ([]byte, bool) {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
		return nil, false
	}
	if outboundCredentialRecipient == nil {
		return unavailable()
	}
	recipient := outboundCredentialRecipient()
	if recipient == nil {
		return unavailable()
	}
	sealed, err := secretbox.SealBytes(recipient, outbound.ManagedAuthorizationSealNamespace, []byte(authorization), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		return unavailable()
	}
	return sealed, true
}

func (s *server) deleteOutboundCredentialKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Key ID must be a UUID")
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundCredentialKey(r.Context(), acct.ID, integrationID, keyID)
	s.writeOutboundAIWriteResult(w, err, "outbound credential key not found", "")
}

// queryLimit reads ?limit=, defaulting to and capping at maximum.
func queryLimit(r *http.Request, maximum int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return maximum, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximum {
		return 0, false
	}
	return limit, true
}

// listOutboundAIBudgetAlerts lists the budget thresholds outboundd recorded,
// newest first (ADR-984).
func (s *server) listOutboundAIBudgetAlerts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(r, api.OutboundAIBudgetAlertsPageMax)
	if !ok {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "limit must be 1 to 100")
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	alerts, err := store.ListOutboundAIBudgetAlerts(r.Context(), acct.ID, integrationID, limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound integration not found")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI budget alerts are unavailable")
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
			out.Items = append(out.Items, item)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}
}

// listOutboundAIRequestLog pages the opt-in request log newest first;
// ?before= takes the previous page's next_before (ADR-984).
func (s *server) listOutboundAIRequestLog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := outboundIntegrationPathID(w, r)
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
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "limit must be 1 to 200 and before an RFC 3339 time")
		return
	}
	store, ok := s.outboundAIControlStore(w)
	if !ok {
		return
	}
	entries, err := store.ListOutboundAIRequestLog(r.Context(), acct.ID, integrationID, before, limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound integration not found")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The outbound AI request log is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, outboundAIRequestLogResponse(entries, limit))
	}
}

func outboundAIRequestLogResponse(entries []state.OutboundAIRequestLogEntry, limit int) api.OutboundAIRequestLogList {
	out := api.OutboundAIRequestLogList{Items: make([]api.OutboundAIRequestLogEntry, 0, len(entries))}
	for _, entry := range entries {
		item := api.OutboundAIRequestLogEntry{
			ID: entry.ID, CreatedAt: entry.CreatedAt, Caller: entry.Caller, Model: entry.Model, Status: entry.Status,
			Outcome: entry.Outcome, InputTokens: entry.InputTokens, CachedInputTokens: entry.CachedInputTokens,
			CacheWriteTokens: entry.CacheWriteTokens, OutputTokens: entry.OutputTokens, CostMillicents: entry.CostMillicents,
			DurationMS: entry.DurationMS, TimeToFirstByteMS: copyOutboundDailyLimit(entry.TTFBMS), Attempts: entry.Attempts,
		}
		if entry.RouteID != "" {
			routeID := entry.RouteID
			item.RouteID = &routeID
		}
		out.Items = append(out.Items, item)
	}
	if len(entries) == limit && len(entries) > 0 {
		next := entries[len(entries)-1].CreatedAt
		out.NextBefore = &next
	}
	return out
}
