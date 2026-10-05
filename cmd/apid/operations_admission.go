package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *server) operationDefinitionAdmission(account, app, scope string) bool {
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if s.operationsPreview != nil {
		return s.operationsPreview.AllowsDefinition(account, app, scope)
	}
	// Existing private fixtures can qualify contracts without startup wiring.
	// configureOperations always installs a closed gate, including defaults.
	return s.operationsAdmissionEnabled
}

func (s *server) operationTenantAdmission(account, app, scope, tenant string) bool {
	if s.operationsPreview != nil {
		return s.operationsWorkloadVerifier != nil && s.operationArtifactStorage != nil && s.operationsPreview.AllowsTenant(account, app, scope, tenant)
	}
	return s.operationsAdmissionEnabled
}

func (s *server) operationAdmissionOpen(w http.ResponseWriter, account, app, scope, tenant string) bool {
	w.Header().Set("Cache-Control", "no-store")
	allowed := s.operationDefinitionAdmission(account, app, scope)
	if tenant != "" {
		allowed = s.operationTenantAdmission(account, app, scope, tenant)
	}
	if !allowed {
		api.WriteProblem(w, api.ErrCapacity("new operation admission is disabled for this preview cohort"))
	}
	return allowed
}
