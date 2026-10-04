// adr: 430 — customers explicitly declare a safe endpoint; configuration sends no traffic.
package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) outboundProbeStore(w http.ResponseWriter, r *http.Request) (state.OutboundBindingProbeStore, string, bool) {
	id, err := uuid.Parse(r.PathValue("integration"))
	if err != nil {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Integration ID must be a UUID")
		return nil, "", false
	}
	_, catalogsAvailable := s.store.(state.OutboundBindingStore)
	store, ok := s.store.(state.OutboundBindingProbeStore)
	if !ok || !catalogsAvailable {
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_probe_unavailable", "Outbound probe policies are unavailable")
		return nil, "", false
	}
	return store, id.String(), true
}
func (s *server) getOutboundProbePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, id, ok := s.outboundProbeStore(w, r)
	if !ok {
		return
	}
	policy, err := store.GetOutboundBindingProbePolicy(r.Context(), acct.ID, id)
	if outboundProbePolicyError(w, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, policy)
}
func (s *server) putOutboundProbePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, id, ok := s.outboundProbeStore(w, r)
	if !ok {
		return
	}
	var policy api.OutboundBindingProbePolicy
	if decodeJSONSized(r, &policy, 4096) != nil || !policy.Valid() {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Probe requires GET or HEAD, a canonical path without a query, and an expected status from 200 to 299")
		return
	}
	offers, err := s.store.(state.OutboundBindingStore).ListOutboundIntegrationOffers(r.Context(), acct.ID)
	if outboundProbePolicyError(w, err) {
		return
	}
	allowed, owned := false, false
	for _, offer := range offers {
		if offer.ID == id && offer.OwnerKind == "customer" {
			owned = true
			allowed = routepolicy.Policy{AllowedMethods: offer.AllowedMethods, AllowedPathPrefixes: offer.AllowedPathPrefixes}.AllowsRequest(policy.Method, policy.Path)
		}
	}
	if !owned {
		outboundBindingProblem(w, http.StatusNotFound, api.CodeNotFound, "Customer integration not found")
		return
	}
	if !allowed {
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Probe must be allowed by a customer-owned integration's route policy")
		return
	}
	if outboundProbePolicyError(w, store.SetOutboundBindingProbePolicy(r.Context(), acct.ID, id, &policy)) {
		return
	}
	writeJSON(w, http.StatusOK, policy)
}
func (s *server) deleteOutboundProbePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, id, ok := s.outboundProbeStore(w, r)
	if !ok {
		return
	}
	if outboundProbePolicyError(w, store.SetOutboundBindingProbePolicy(r.Context(), acct.ID, id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func outboundProbePolicyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		outboundBindingProblem(w, http.StatusNotFound, api.CodeNotFound, "Customer integration or probe policy not found")
	case errors.Is(err, state.ErrInvalidArgument):
		outboundBindingProblem(w, http.StatusBadRequest, api.CodeValidation, "Invalid outbound probe policy")
	default:
		outboundBindingProblem(w, http.StatusServiceUnavailable, "outbound_probe_unavailable", "Outbound probe policy could not be read or saved")
	}
	return true
}
