package apidserver

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) apiSandboxComparisonRelease(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.Deployment, state.APISandboxComparisonStore, bool) {
	parent, candidate, _, ok := s.apiSandboxRunRelease(w, r, acct)
	if !ok {
		return state.App{}, state.Deployment{}, nil, false
	}
	store, ok := s.store.(state.APISandboxComparisonStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox release comparisons are unavailable"))
	}
	return parent, candidate, store, ok
}

func (s *server) listAPISandboxComparisons(w http.ResponseWriter, r *http.Request, acct state.Account) {
	parent, candidate, store, ok := s.apiSandboxComparisonRelease(w, r, acct)
	if !ok {
		return
	}
	comparisons, err := store.ListAPISandboxComparisons(r.Context(), candidate.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list sandbox release comparisons"))
		return
	}
	response := api.APISandboxComparisonsResponse{Comparisons: comparisons, Baselines: []api.APISandboxComparisonBaseline{}}
	if apiSandboxSourceEligible(parent, candidate) {
		response.Baselines, err = store.ListAPISandboxComparisonBaselines(r.Context(), candidate)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list eligible baseline releases"))
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) getAPISandboxComparison(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, candidate, store, ok := s.apiSandboxComparisonRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "comparison")
	if id == "" {
		s.notFound(w, "no such sandbox comparison")
		return
	}
	report, err := store.GetAPISandboxComparison(r.Context(), id)
	switch {
	case errors.Is(err, state.ErrNotFound), err == nil && report.CandidateDeploymentID != candidate.ID:
		s.notFound(w, "no such sandbox comparison for this release")
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox comparison"))
	default:
		writeJSON(w, http.StatusOK, report)
	}
}

func (s *server) createAPISandboxComparison(w http.ResponseWriter, r *http.Request, acct state.Account) {
	parent, candidate, store, ok := s.apiSandboxComparisonRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "comparison")
	var input api.APISandboxComparisonRequest
	if id == "" || decodeJSONSized(r, &input, 4096) != nil ||
		!canonicalSandboxComparisonID(input.BaselineDeploymentID) || !canonicalSandboxComparisonID(input.ScenarioRevision) ||
		input.BaselineDeploymentID == candidate.ID {
		api.WriteProblem(w, api.ErrValidation("use lowercase UUIDs for the comparison, a different baseline release, and the saved scenario revision"))
		return
	}
	ctx := r.Context()
	if existing, err := store.GetAPISandboxComparison(ctx, id); err == nil {
		s.writeExistingSandboxComparison(w, existing, candidate.ID, input)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check an existing sandbox comparison"))
		return
	}
	if !s.apiSandboxWorkerEnabled() {
		api.WriteProblem(w, api.ErrCapacity("scenario provisioning is unavailable"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, parent) || !supportedAPISandboxSource(w, parent, candidate) {
		return
	}
	baseline, ok := s.readAPISandboxSource(w, r, parent, input.BaselineDeploymentID)
	if !ok || !supportedAPISandboxSource(w, parent, baseline) {
		return
	}
	scenario, err := s.selectedAPISandboxScenario(ctx, parent.ID, candidate.ID, input.ScenarioRevision)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
		return
	}
	if scenario == nil || scenario.Revision != input.ScenarioRevision {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox scenario changed", "reload the scenario selection before comparing releases"))
		return
	}

	apps, problem := s.buildAPISandboxComparisonApps(acct, parent, baseline.ID, candidate.ID, scenario)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := store.CreateAPISandboxComparison(ctx, id, apps[0], apps[1], api.MustLimitsFor(acct.Plan))
	if err != nil {
		// A concurrent retry can have committed the pair before this request
		// reached quota admission, so resolve the comparison before the error.
		if existing, readErr := store.GetAPISandboxComparison(ctx, id); readErr == nil {
			s.writeExistingSandboxComparison(w, existing, candidate.ID, input)
			return
		}
		if writeAPISandboxQuota(w, acct, err) {
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not reserve both scenario sandboxes"))
		return
	}
	for _, app := range created {
		s.emitAppCreated(ctx, app)
	}
	s.audit.Emit(ctx, "api_sandbox.comparison_created", &acct.ID, map[string]any{
		"comparison_id": id, "baseline_deployment_id": baseline.ID, "candidate_deployment_id": candidate.ID,
	})
	report, err := store.GetAPISandboxComparison(ctx, id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the queued sandbox comparison"))
		return
	}
	writeJSON(w, http.StatusAccepted, report)
}

func (s *server) buildAPISandboxComparisonApps(acct state.Account, parent state.App, baselineID, candidateID string, scenario *api.APISandboxScenario) ([2]state.App, *api.Problem) {
	var apps [2]state.App
	for i, sourceID := range [2]string{baselineID, candidateID} {
		runID := uuid.NewString()
		app, problem := s.buildAPISandboxApp(acct, parent, sourceID, apiSandboxSlug(runID))
		if problem != nil {
			return [2]state.App{}, problem
		}
		app.APISandboxRunID, app.APISandboxScenario, app.Manifest.APISandboxRun = runID, scenario, true
		apps[i] = app
	}
	return apps, nil
}

func canonicalSandboxComparisonID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (s *server) writeExistingSandboxComparison(w http.ResponseWriter, report api.APISandboxComparisonReport, candidateID string, input api.APISandboxComparisonRequest) {
	if report.CandidateDeploymentID != candidateID {
		s.notFound(w, "no such sandbox comparison for this release")
		return
	}
	if report.BaselineDeploymentID != input.BaselineDeploymentID || report.ScenarioRevision != input.ScenarioRevision {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox comparison already configured", "retry with the original baseline and scenario revision or start a new comparison"))
		return
	}
	writeJSON(w, http.StatusOK, report)
}
