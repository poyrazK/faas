package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) projectVerificationRunBackend(w http.ResponseWriter, r *http.Request, acct state.Account) (bindings.ProjectVerificationRunBackend, bool) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return bindings.ProjectVerificationRunBackend{}, false
	}
	store, ok := s.store.(state.ProjectBindingVerificationRunStore)
	catalog, catalogOK := s.store.(bindings.ProjectReleaseCatalog)
	_, runOK := s.store.(state.BindingVerificationRunStore)
	_, taskOK := s.store.(state.AppTaskStore)
	_, queueOK := s.store.(state.QueueBindingCanaryStore)
	if !ok || !catalogOK || !runOK || !taskOK || !queueOK {
		api.WriteProblem(w, api.ErrCapacity("durable graph verification is unavailable"))
		return bindings.ProjectVerificationRunBackend{}, false
	}
	return bindings.ProjectVerificationRunBackend{Store: store, Catalog: catalog, Project: project, AccountID: acct.ID, Environment: r.PathValue("environment"), Member: func(ctx context.Context, slug, appID string) (bindings.VerificationRunBackend, error) {
		app, err := s.store.AppByID(ctx, appID)
		if err != nil {
			return bindings.VerificationRunBackend{}, err
		}
		if app.AccountID != acct.ID {
			return bindings.VerificationRunBackend{}, state.ErrConflict
		}
		return s.bindingVerificationBackendForApp(r, acct, app), nil
	}}, true
}
func (s *server) redactProjectVerificationRun(r *http.Request, run api.ProjectBindingVerificationRun) api.ProjectBindingVerificationRun {
	for name, member := range run.Members {
		run.Members[name] = s.redactVerificationRun(r, member)
	}
	return run
}
func (s *server) createProjectBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.projectVerificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var in api.CreateProjectBindingVerificationRunRequest
	if err := decodeJSONSized(r, &in, api.ProjectBindingVerificationRunRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid graph verification request"))
		return
	}
	run, err := backend.Create(r.Context(), in)
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "bindings.graph_verification_run_created", &acct.ID, map[string]any{"run_id": run.ID, "project_id": backend.Project.ID})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, s.redactProjectVerificationRun(r, run))
}
func (s *server) handleProjectBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	backend, ok := s.projectVerificationRunBackend(w, r, acct)
	if !ok {
		return
	}
	var run api.ProjectBindingVerificationRun
	var err error
	switch {
	case r.Method == http.MethodDelete:
		run, err = backend.Cancel(r.Context(), r.PathValue("run"))
	case r.Method == http.MethodGet:
		run, err = backend.Status(r.Context(), r.PathValue("run"))
	case r.PathValue("action") == "resume":
		run, err = backend.Resume(r.Context(), r.PathValue("run"))
	case r.PathValue("action") == "complete":
		run, err = backend.Complete(r.Context(), r.PathValue("run"))
	default:
		err = state.ErrInvalidArgument
	}
	if problem := bindings.VerificationRunProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if r.Method != http.MethodGet {
		s.audit.Emit(r.Context(), "bindings.graph_verification_run_changed", &acct.ID, map[string]any{"run_id": run.ID, "status": run.Status, "project_id": backend.Project.ID})
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if r.PathValue("action") == "complete" && run.Status == "pending" && run.Qualification != nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, s.redactProjectVerificationRun(r, run))
}

func (s *server) resumeProjectBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("action", "resume")
	s.handleProjectBindingVerificationRun(w, r, acct)
}
func (s *server) completeProjectBindingVerificationRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("action", "complete")
	s.handleProjectBindingVerificationRun(w, r, acct)
}
