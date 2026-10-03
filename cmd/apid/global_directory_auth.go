package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) personalDirectoryOrg(ctx context.Context, accountID, orgID string) (bool, error) {
	if orgID == "" {
		return true, nil
	} // Legacy personally owned rows.
	org, err := s.store.OrgByID(ctx, orgID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == accountID && org.Status == state.OrgStatusActive && !org.DeletedPending, nil
}

func (s *server) requireDirectoryAccount(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		_, key, _ := middleware.AccountFromContext(r)
		if key != nil {
			personal, err := s.personalDirectoryOrg(r.Context(), acct.ID, key.OrgID)
			if err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not verify personal key ownership"))
				return
			}
			if !personal || key.AppID != "" || key.PlatformTenantID != "" {
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Personal account principal required", "Global directory identities are currently scoped to personal accounts."))
				return
			}
		}
		next(w, r, acct)
	}
}

func (s *server) requireRegionalAccountKey(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		_, key, _ := middleware.AccountFromContext(r)
		if key == nil {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Regional account key required", "Ownership verification requires a regional account-admin API key."))
			return
		}
		next(w, r, acct)
	}
}
