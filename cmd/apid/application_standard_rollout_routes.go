package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applicationStandardRolloutRoute(kind string, handler accountHandler) http.HandlerFunc {
	return s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.requireApplicationStandardRolloutMutation(kind, s.idempotent(handler))))))
}

// Release admission, current role and organization-scoped resource ownership
// precede cached response lookup. The durable mutations recheck authority.
func (s *server) requireApplicationStandardRolloutMutation(kind string, next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.applicationStandardMutationsAvailable(w) {
			return
		}
		orgID, id, ok := s.applicationStandardRolloutIdentity(w, r, kind)
		if !ok {
			return
		}
		if err := s.applicationStandardRolloutResource(r.Context(), orgID, id, kind); err != nil {
			writeApplicationStandardRolloutError(w, err)
			return
		}
		next(w, r, acct)
	}
}

func (s *server) applicationStandardRolloutIdentity(w http.ResponseWriter, r *http.Request, kind string) (string, string, bool) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionApproveApplicationStandards)
	if !ok {
		return "", "", false
	}
	id, ok := standardPathUUID(w, r, kind)
	return orgID, id, ok
}

func (s *server) applicationStandardRolloutResource(ctx context.Context, orgID, id, kind string) error {
	if kind == "review" {
		store, ok := s.store.(state.ApplicationStandardReviewStore)
		if !ok {
			return errors.New("application standard reviews unavailable")
		}
		_, err := store.GetApplicationStandardReviewPlan(ctx, orgID, id)
		return err
	}
	store, ok := s.store.(state.ApplicationStandardOperationStore)
	if !ok {
		return errors.New("application standard operations unavailable")
	}
	_, err := store.GetApplicationStandardOperation(ctx, orgID, id)
	return err
}
