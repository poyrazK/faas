package main

// adr: 611
import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) requireGlobalTargetConsent(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if globalOwner(r, acct).Owner.Shared() && !s.requireOrgAction(w, r, authz.OrgActionCreateApiKey) {
			return
		}
		next(w, r, acct)
	}
}

func (s *server) commitGlobalTarget(r *http.Request, acct state.Account, b globaldirectory.Binding) (globaldirectory.Target, error) {
	p := globalOwner(r, acct)
	if !p.Owner.Shared() {
		return s.globalDirectory.AttachTarget(r.Context(), p.Owner.ID, r.PathValue("slug"), b)
	}
	store, ok := s.globalDirectory.(globaldirectory.OrgTargetStore)
	if !ok {
		return globaldirectory.Target{}, globaldirectory.ErrOrgTargetsUnavailable
	}
	return store.AttachOrgTarget(r.Context(), p.Owner.ID, acct.ID, r.PathValue("slug"), b)
}

func (s *server) removeGlobalTarget(r *http.Request, acct state.Account) error {
	p := globalOwner(r, acct)
	if !p.Owner.Shared() {
		return s.globalDirectory.DetachTarget(r.Context(), p.Owner.ID, r.PathValue("slug"), r.PathValue("region"))
	}
	store, ok := s.globalDirectory.(globaldirectory.OrgTargetStore)
	if !ok {
		return globaldirectory.ErrOrgTargetsUnavailable
	}
	return store.DetachOrgTarget(r.Context(), p.Owner.ID, acct.ID, r.PathValue("slug"), r.PathValue("region"))
}
