package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// createOrgApp creates an app whose persisted organization is the
// LoadOrg-verified workspace. The creator remains the app's account owner
// until the broader app API authorization and billing migration is complete.
func (s *server) createOrgApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireOrgAction(w, r, authz.OrgActionCreateApp) {
		return
	}
	mem, ok := s.requireMembership(w, r)
	if !ok {
		return
	}
	s.createAppInOrg(w, r, acct, mem.OrgID)
}
