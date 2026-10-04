package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
)

func (s *server) applicationStandardMutationRoute(action authz.OrgAction, handler accountHandler) http.HandlerFunc {
	return s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.requireApplicationStandardMutations(action, s.idempotent(handler))))))
}
