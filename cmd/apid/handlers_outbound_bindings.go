package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the fleet recipient is valid for outbound credentials: outboundd may
// run on a different host and receives the fleet private key, never a host key.
var outboundCredentialRecipient func() *age.X25519Recipient

func outboundOfferResponse(offer state.OutboundIntegrationOffer) api.OutboundIntegrationOffer {
	return api.OutboundIntegrationOffer{
		ID: offer.ID, Name: offer.Name, Origin: offer.Origin,
		AllowedMethods:       append([]string{}, offer.AllowedMethods...),
		AllowedPathPrefixes:  append([]string{}, offer.AllowedPathPrefixes...),
		Enabled:              offer.Enabled,
		CredentialSource:     offer.CredentialSource,
		CredentialConfigured: offer.CredentialConfigured,
		OwnerKind:            offer.OwnerKind,
		DailyRequestLimit:    copyOutboundDailyLimit(offer.DailyRequestLimit),
	}
}

func copyOutboundDailyLimit(limit *int64) *int64 {
	if limit == nil {
		return nil
	}
	copy := *limit
	return &copy
}

func outboundBindingResponse(binding state.OutboundAppBinding) api.OutboundAppBinding {
	return api.OutboundAppBinding{
		Integration: outboundOfferResponse(binding.OutboundIntegrationOffer),
		AppID:       binding.AppID, CreatedAt: binding.CreatedAt,
		AllowedMethods:      append([]string{}, binding.RouteMethods...),
		AllowedPathPrefixes: append([]string{}, binding.RoutePathPrefixes...),
	}
}

func (s *server) outboundBindingStore(w http.ResponseWriter) (state.OutboundBindingStore, bool) {
	store, ok := s.store.(state.OutboundBindingStore)
	if !ok {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound bindings are unavailable")
	}
	return store, ok
}

func outboundBindingProblem(w http.ResponseWriter, status int, code, detail string) {
	api.WriteProblem(w, api.NewProblem(status, code, http.StatusText(status), detail))
}

func (s *server) listOutboundIntegrationOffers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	offers, err := store.ListOutboundIntegrationOffers(r.Context(), acct.ID)
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound integrations are unavailable")
		return
	}
	items := make([]api.OutboundIntegrationOffer, 0, len(offers))
	for _, offer := range offers {
		items = append(items, outboundOfferResponse(offer))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.OutboundIntegrationOfferList{Items: items})
}

func (s *server) createOutboundIntegration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateOutboundIntegrationRequest
	if err := decodeJSONSized(r, &req, 32<<10); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "A valid outbound integration is required")
		return
	}
	if req.DailyRequestLimit != nil {
		maximum, ok := api.OutboundRequestsPerDayMaxForPlan(acct.Plan)
		if !ok || *req.DailyRequestLimit < 1 || *req.DailyRequestLimit > maximum {
			outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Daily request limit exceeds the account plan ceiling")
			return
		}
	}
	origin, err := url.Parse(req.Origin)
	if err != nil || strings.ContainsAny(req.Origin, "?#") || outbound.ValidatePublicOrigin(r.Context(), origin) != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Origin must resolve only to public addresses over HTTPS")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	offer := state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: req.Name, Origin: origin.String(),
		AllowedMethods:      append([]string(nil), req.AllowedMethods...),
		AllowedPathPrefixes: append([]string(nil), req.AllowedPathPrefixes...),
		DailyRequestLimit:   copyOutboundDailyLimit(req.DailyRequestLimit),
		Enabled:             true, CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: "customer",
	}
	created, err := store.CreateOutboundIntegration(r.Context(), offer)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration name, methods, or paths are invalid")
	case errors.Is(err, state.ErrConflict):
		outboundBindingProblem(w, http.StatusConflict, api.CodeConflict, "An outbound integration with this name already exists")
	case errors.Is(err, state.ErrOutboundIntegrationLimit):
		outboundBindingProblem(w, http.StatusTooManyRequests, "outbound_integration_limit", "The account has reached its outbound integration limit")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound integration could not be created")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, outboundOfferResponse(created))
	}
}

func (s *server) deleteOutboundIntegration(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	if err := store.DeleteOutboundIntegration(r.Context(), acct.ID, integrationID); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "customer outbound integration not found")
		return
	} else if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound integration could not be deleted")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getOutboundIntegrationUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	usage, err := store.GetOutboundIntegrationUsage(r.Context(), acct.ID, integrationID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "outbound integration not found")
		return
	}
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_usage_unavailable", "Outbound integration usage is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.OutboundIntegrationUsageResponse{
		DailyRequestCount: usage.DailyRequestCount,
		DailyRequestLimit: copyOutboundDailyLimit(usage.DailyRequestLimit),
		UsageDate:         usage.UsageDate,
		ResetsAt:          usage.ResetsAt,
	})
}

func (s *server) putOutboundIntegrationDailyBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	var req api.PutOutboundDailyRequestBudgetRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "A daily request budget is required")
		return
	}
	if req.DailyRequestLimit != nil {
		maximum, ok := api.OutboundRequestsPerDayMaxForPlan(acct.Plan)
		if !ok || *req.DailyRequestLimit < 1 || *req.DailyRequestLimit > maximum {
			outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Daily request limit exceeds the account plan ceiling")
			return
		}
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	err := store.SetOutboundDailyRequestLimit(r.Context(), acct.ID, integrationID, req.DailyRequestLimit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "customer outbound integration not found")
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Daily request limit exceeds the account plan ceiling")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_budget_unavailable", "Outbound daily request budget could not be updated")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) listOutboundAppBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	bindings, err := store.ListOutboundAppBindings(r.Context(), acct.ID, app.ID)
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound bindings are unavailable")
		return
	}
	items := make([]api.OutboundAppBinding, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, outboundBindingResponse(binding))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.OutboundAppBindingList{Items: items})
}

func (s *server) putOutboundAppBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	binding, err := store.BindOutboundIntegration(r.Context(), acct.ID, app.ID, integrationID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "outbound integration not found")
		return
	}
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound binding could not be created")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, outboundBindingResponse(binding))
}

func (s *server) deleteOutboundAppBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	if err := store.UnbindOutboundIntegration(r.Context(), acct.ID, app.ID, integrationID); err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound binding could not be deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) updateOutboundBindingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	var req api.UpdateOutboundBindingPolicyRequest
	if err := decodeJSONSized(r, &req, 32<<10); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "A valid binding policy is required")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	err := store.UpdateOutboundBindingPolicy(r.Context(), acct.ID, app.ID, integrationID, req.AllowedMethods, req.AllowedPathPrefixes)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "outbound binding not found")
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Binding policy must narrow the integration's methods and paths")
	case err != nil:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_binding_unavailable", "Outbound binding policy could not be updated")
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) putOutboundCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	var req api.PutOutboundCredentialRequest
	if err := decodeJSONSized(r, &req, 10<<10); err != nil || !outbound.ValidManagedAuthorization(req.Authorization) {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "A valid Authorization value is required")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	if outboundCredentialRecipient == nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
		return
	}
	recipient := outboundCredentialRecipient()
	if recipient == nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
		return
	}
	sealed, err := secretbox.SealBytes(recipient, outbound.ManagedAuthorizationSealNamespace,
		[]byte(req.Authorization), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
		return
	}
	if err := store.SetOutboundCredential(r.Context(), acct.ID, integrationID, sealed); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "outbound integration not found")
		return
	} else if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential could not be saved")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteOutboundCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID := r.PathValue("integration")
	if _, err := uuid.Parse(integrationID); err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return
	}
	store, ok := s.outboundBindingStore(w)
	if !ok {
		return
	}
	if err := store.DeleteOutboundCredential(r.Context(), acct.ID, integrationID); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "outbound integration not found")
		return
	} else if err != nil {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_credential_unavailable", "Outbound credential could not be deleted")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
