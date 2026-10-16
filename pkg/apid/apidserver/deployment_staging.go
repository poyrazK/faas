package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) deploymentStagingStore(w http.ResponseWriter) (state.DeploymentStagingStore, bool) {
	store, ok := s.store.(state.DeploymentStagingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deployment policy staging is unavailable"))
	}
	return store, ok
}

func (s *server) getDeploymentStagingSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deploymentStagingStore(w)
	if !ok {
		return
	}
	snapshot, err := store.DeploymentStagingSnapshot(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.writeDeploymentStagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *server) applyDeploymentStaging(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.DeploymentStagingRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := request.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.deploymentStagingStore(w)
	if !ok {
		return
	}
	result, err := store.ApplyDeploymentStaging(r.Context(), app.ID, state.DeploymentStagingParams{
		AccountID: acct.ID, Request: request,
		Prepare: func(current state.App, policies map[string]json.RawMessage) (state.AppManifest, error) {
			return s.prepareStagedPolicies(current, policies, acct)
		},
	})
	if err != nil {
		s.writeDeploymentStagingError(w, err)
		return
	}
	if result.Changed {
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged, app.ID)
		fields := make([]string, 0, len(request.Desired))
		for field := range request.Desired {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		s.audit.Emit(r.Context(), "app.updated", &acct.ID, map[string]any{"app_id": app.ID, "source": "deploy_staging", "operation": request.Operation, "fields": fields})
	}
	writeJSON(w, http.StatusOK, result.Receipt)
}

func (s *server) prepareStagedPolicies(app state.App, policies map[string]json.RawMessage, acct state.Account) (state.AppManifest, error) {
	data, err := json.Marshal(policies)
	if err != nil {
		return app.Manifest, err
	}
	var request api.UpdateAppRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return app.Manifest, err
	}
	if request.Profiling != nil && request.Profiling.Enabled && s.profileBackend == nil {
		return app.Manifest, &api.APIError{Problem: *api.ErrCapacity("CPU profiling is unavailable on this installation")}
	}
	if request.Tracing != nil && request.Tracing.Enabled && !s.guestTracingEnabled {
		return app.Manifest, &api.APIError{Problem: *api.ErrCapacity("request tracing is unavailable on this installation")}
	}
	if problem := validateUpdateApp(&request, acct, api.MustLimitsFor(acct.Plan), app); problem != nil {
		return app.Manifest, &api.APIError{Problem: *problem}
	}
	manifest := app.Manifest
	if _, ok := policies["profiling"]; ok {
		manifest.Profiling = request.Profiling
	}
	if _, ok := policies["tracing"]; ok {
		manifest.Tracing = request.Tracing
	}
	return manifest, nil
}

func (s *server) writeDeploymentStagingError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrDeploymentStagingConflict) {
		problem := api.NewProblem(http.StatusConflict, api.CodeDeploymentStagingConflict, "Deployment policy staging changed", "app, deployment or policy ownership changed; cleanup remains pending")
		problem.Hint = "Inspect the saved deploy and current policies before continuing; no policies were overwritten."
		api.WriteProblem(w, problem)
	} else if problem := api.AsProblem(err); problem != nil {
		api.WriteProblem(w, problem)
	} else if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app")
	} else if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("invalid deployment staging request"))
	} else {
		api.WriteProblem(w, api.ErrCapacity("could not apply deployment policy staging"))
	}
}
