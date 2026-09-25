package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func outboundOfferResponse(offer state.OutboundIntegrationOffer) api.OutboundIntegrationOffer {
	return api.OutboundIntegrationOffer{
		ID: offer.ID, Name: offer.Name, Origin: offer.Origin,
		AllowedMethods:      append([]string{}, offer.AllowedMethods...),
		AllowedPathPrefixes: append([]string{}, offer.AllowedPathPrefixes...),
		Enabled:             offer.Enabled,
	}
}

func outboundBindingResponse(binding state.OutboundAppBinding) api.OutboundAppBinding {
	return api.OutboundAppBinding{
		Integration: outboundOfferResponse(binding.OutboundIntegrationOffer),
		AppID:       binding.AppID, CreatedAt: binding.CreatedAt,
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
