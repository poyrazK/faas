package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardKeyDeleteAction     = "key_delete"
	dashboardKeyDeleteCSRFCookie = "faas_csrf_key_delete"
	dashboardKeyConfirmField     = "confirmation"
)

// dashboardDeleteKey handles the account-page API-key revoke form. The key
// lookup is account-scoped, and the customer must type the displayed key
// prefix before the shared REST/dashboard revocation core is called.
func (s *server) dashboardDeleteKey(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardKeyDeleteAction, acct.ID, dashboardKeyDeleteCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	key, err := s.store.GetAPIKey(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.writeDashboardKeyError(w, err)
		return
	}
	want := api.APIKeyPrefix + hexPrefix(key.Hash)
	if r.PostForm.Get(dashboardKeyConfirmField) != want {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Confirmation does not match", "type the displayed API key prefix exactly"))
		return
	}
	if _, err := s.revokeAPIKey(r.Context(), acct, key.ID); err != nil {
		s.writeDashboardKeyError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/account?key_revoked=1", http.StatusFound)
}

func (s *server) writeDashboardKeyError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such key")
		return
	}
	api.WriteProblem(w, api.ErrCapacity("could not revoke key"))
}
