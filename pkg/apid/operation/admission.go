package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func (s *Service) OperationDefinitionsAdmission(account, app, scope string, specs []api.OperationDefinitionSpec) bool {
	if len(specs) == 0 {
		return true
	}
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if s.Preview != nil {
		kinds := make([]string, 0, len(specs))
		for _, spec := range specs {
			kinds = append(kinds, operations.DefinitionExecutionKind(spec))
		}
		return s.Preview.AllowsDefinitionKinds(account, app, scope, kinds)
	}
	// Existing private fixtures can qualify contracts without startup wiring.
	// configureOperations always installs a closed gate, including defaults.
	return s.AdmissionEnabled
}

func (s *Service) OperationTenantKindAdmission(account, app, scope, tenant, kind string) bool {
	if s.Preview != nil {
		return s.WorkloadVerifier != nil && s.ArtifactStorage != nil && s.Preview.AllowsTenantKind(account, app, scope, tenant, kind)
	}
	return s.AdmissionEnabled
}

func (s *Service) operationAdmissionOpen(w http.ResponseWriter, account, app, scope, tenant string, spec api.OperationDefinitionSpec) bool {
	w.Header().Set("Cache-Control", "no-store")
	allowed := s.OperationDefinitionsAdmission(account, app, scope, []api.OperationDefinitionSpec{spec})
	if tenant != "" {
		allowed = s.OperationTenantKindAdmission(account, app, scope, tenant, operations.DefinitionExecutionKind(spec))
	}
	if !allowed {
		api.WriteProblem(w, api.ErrCapacity("new operation admission is disabled for this preview cohort"))
	}
	return allowed
}
