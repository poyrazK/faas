package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func verificationDiagnosticsAccess(r *http.Request, run api.BindingVerificationRun) error {
	postgres, storage := false, len(run.Policy.ObjectStorageRequirements) > 0
	for _, item := range run.Inventory.Bindings {
		postgres = postgres || item.Type == api.BindingTypePostgres
		storage = storage || item.Type == api.BindingTypeObjectStorage
	}
	for _, step := range run.Steps {
		postgres = postgres || step.Plan.Type == api.BindingTypePostgres
		storage = storage || step.Plan.Type == api.BindingTypeObjectStorage
	}
	if postgres && !middleware.HasScope(r, api.ScopesManagedPostgresReadSurface...) || storage && !middleware.HasScope(r, api.ScopesStorageManageSurface...) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Diagnostics unavailable", "Read access to every reviewed binding domain is required.")
	}
	return nil
}
func (s *server) getBindingVerificationRunDiagnostics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.verificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	diagnostic, err := backend.Diagnose(r.Context(), r.PathValue("run"))
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, diagnostic)
}
func (s *server) getProjectBindingVerificationRunDiagnostics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.projectVerificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	diagnostic, err := backend.Diagnose(r.Context(), r.PathValue("run"))
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, diagnostic)
}
