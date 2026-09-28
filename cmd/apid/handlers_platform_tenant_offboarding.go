package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) planPlatformTenantOffboarding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantOffboardingStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant offboarding planning is unavailable"))
		return
	}
	plan, err := store.PlanPlatformTenantOffboarding(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not plan platform tenant offboarding"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, plan)
}
