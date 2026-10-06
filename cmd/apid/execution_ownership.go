package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type executionAccess struct {
	broad       bool
	principalID string
	principal   *string
}

// executionAccessForRequest preserves account-wide behavior for sessions and
// legacy broad-scope keys while fencing narrow Runs credentials to a key family.
func executionAccessForRequest(r *http.Request) (executionAccess, *api.Problem) {
	_, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return executionAccess{}, api.ErrCapacity("execution authorization context is unavailable")
	}
	if key == nil {
		return executionAccess{broad: true}, nil
	}
	access := executionAccess{principalID: key.RunsPrincipalID}
	for _, scope := range key.Scopes {
		if scope == api.ScopeAdmin || scope == api.ScopeAppsRead || scope == api.ScopeDeployWrite {
			access.broad = true
			break
		}
	}
	if key.RunsPrincipalID != "" {
		principalID := key.RunsPrincipalID
		access.principal = &principalID
	}
	if !access.broad && access.principalID == "" {
		return executionAccess{}, api.ErrCapacity("Runs API key is missing its stable ownership identity")
	}
	return access, nil
}

func (a executionAccess) owns(row state.Execution) bool {
	return a.broad || (row.RunsPrincipalID != nil && *row.RunsPrincipalID == a.principalID)
}

func writeExecutionAccessError(w http.ResponseWriter, problem *api.Problem) {
	api.WriteProblem(w, problem)
}

func requireExecutionOwnership(w http.ResponseWriter, s *server, access executionAccess, row state.Execution) bool {
	if access.owns(row) {
		return true
	}
	s.notFound(w, "no such execution")
	return false
}
