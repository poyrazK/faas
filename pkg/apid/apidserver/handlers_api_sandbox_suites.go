package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) sandboxSuiteStore(w http.ResponseWriter) (state.APISandboxSuiteStore, bool) {
	store, ok := s.store.(state.APISandboxSuiteStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox regression suites are unavailable"))
	}
	return store, ok
}

func (s *server) getAPISandboxSuite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.sandboxSuiteStore(w)
	if !ok {
		return
	}
	suite, err := store.GetAPISandboxSuite(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusOK, api.APISandboxSuiteResponse{})
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox suite"))
		return
	}
	writeJSON(w, http.StatusOK, api.APISandboxSuiteResponse{Suite: &suite})
}

func (s *server) putAPISandboxSuite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok || !s.allowAPISandboxProvisioning(w, r, acct, app) {
		return
	}
	store, ok := s.sandboxSuiteStore(w)
	if !ok {
		return
	}
	var input api.APISandboxSuiteSaveRequest
	if decodeJSONSized(r, &input, 4096) != nil || input.ExpectedRevision == nil ||
		(*input.ExpectedRevision != "" && !canonicalSandboxComparisonID(*input.ExpectedRevision)) ||
		!validAPISandboxSuiteIDs(input.ScenarioIDs) {
		api.WriteProblem(w, api.ErrValidation("supply expected_revision and up to five distinct library scenario UUIDs"))
		return
	}
	suite, err := store.PutAPISandboxSuite(r.Context(), app.ID, *input.ExpectedRevision, input.ScenarioIDs, input.RunBeforeApproval)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox suite changed",
			"reload the suite and select scenarios from this app's current library"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not save the sandbox suite"))
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.suite_saved", &acct.ID, map[string]any{"app_id": app.ID, "suite_revision": suite.Revision})
	writeJSON(w, http.StatusOK, api.APISandboxSuiteResponse{Suite: &suite})
}

func validAPISandboxSuiteIDs(ids []string) bool {
	if ids == nil || len(ids) > api.APISandboxSuiteMaxScenarios {
		return false
	}
	for i, id := range ids {
		if !canonicalSandboxComparisonID(id) || slices.Contains(ids[:i], id) {
			return false
		}
	}
	return true
}

func (s *server) listAPISandboxSuiteRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	candidate, app, _, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.sandboxSuiteStore(w)
	if !ok {
		return
	}
	runs, err := store.ListAPISandboxSuiteRuns(r.Context(), candidate.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list the sandbox suite runs"))
		return
	}
	result := api.APISandboxSuiteRunsResponse{Runs: runs}
	if automatic, ok := s.store.(state.APISandboxSuiteAutomaticStore); ok {
		result.AutomaticStatus, err = automatic.GetAPISandboxSuiteAutomaticStatus(r.Context(), candidate.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not read the automatic sandbox suite"))
			return
		}
	}
	if reader, ok := s.store.(state.APISandboxReleaseGateReader); ok {
		result.ReleaseGate, err = s.readAPISandboxReleaseGate(r.Context(), reader, app, candidate)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the sandbox release requirement"))
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) readAPISandboxReleaseGate(ctx context.Context, reader state.APISandboxReleaseGateReader, app state.App, candidate state.Deployment) (*api.APISandboxReleaseGate, error) {
	wait, err := approval.LoadReleaseCommandWait(ctx, s.store, app, candidate, time.Now())
	if err != nil {
		return nil, err
	}
	atApproval := false
	if wait.Status.RequirePassingSandboxSuite {
		_, err := s.store.ReleaseAppTaskByDeployment(ctx, candidate.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		atApproval = errors.Is(err, state.ErrNotFound)
	}
	return reader.APISandboxReleaseGate(ctx, app.ID, candidate.ID, atApproval)
}

func (s *server) getAPISandboxSuiteRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	candidate, app, checks, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.sandboxSuiteStore(w)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "run")
	if id == "" {
		s.notFound(w, "no such sandbox suite run")
		return
	}
	run, err := store.GetAPISandboxSuiteRun(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such sandbox suite run")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox suite run"))
		return
	}
	s.writeAPISandboxSuiteRun(w, r, app, candidate, checks, store, run, nil, http.StatusOK)
}

func (s *server) createAPISandboxSuiteRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	candidate, app, checks, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.sandboxSuiteStore(w)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "run")
	var input api.APISandboxSuiteRunRequest
	if id == "" || decodeJSONSized(r, &input, 4096) != nil ||
		!canonicalSandboxComparisonID(input.BaselineDeploymentID) || !canonicalSandboxComparisonID(input.SuiteRevision) {
		api.WriteProblem(w, api.ErrValidation("use UUIDs for the run, suite_revision, and baseline_deployment_id"))
		return
	}
	ctx := r.Context()
	if existing, err := store.GetAPISandboxSuiteRun(ctx, id); err == nil {
		s.writeAPISandboxSuiteRun(w, r, app, candidate, checks, store, existing, &input, http.StatusOK)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check for an existing suite run"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, app) {
		return
	}
	in, err := s.sandboxApprovalInputs(ctx, app, candidate, false)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the current sandbox approval inputs"))
		return
	}
	if in.availability != api.APISandboxApprovalReady || input.BaselineDeploymentID != in.baselineID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox suite is unavailable",
			"refresh the approval page to check the held build and live baseline"))
		return
	}
	run, err := store.CreateAPISandboxSuiteRun(ctx, newSandboxSuiteRun(app.ID, candidate.ID, id, input.SuiteRevision, in, time.Now().UTC(), false))
	if err != nil {
		if existing, readErr := store.GetAPISandboxSuiteRun(ctx, id); readErr == nil {
			s.writeAPISandboxSuiteRun(w, r, app, candidate, checks, store, existing, &input, http.StatusOK)
		} else if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Could not queue the sandbox suite",
				"reload the saved suite and wait for any active suite run on this app"))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not queue the sandbox suite"))
		}
		return
	}
	s.auditSandboxSuiteQueued(ctx, acct.ID, run)
	s.writeAPISandboxSuiteRun(w, r, app, candidate, checks, store, run, nil, http.StatusAccepted)
}

func newSandboxSuiteRun(appID, candidateID, id, revision string, in sandboxApprovalInputs, now time.Time, automatic bool) state.APISandboxSuiteRun {
	return state.APISandboxSuiteRun{
		AppID: appID, Pins: in.pins, DeadlineAt: now.Add(apiSandboxSuiteTimeout),
		APISandboxSuiteRunSummary: api.APISandboxSuiteRunSummary{
			ID: id, SuiteRevision: revision, BaselineDeploymentID: in.baselineID,
			CandidateDeploymentID: candidateID, CreatedAt: now, Automatic: automatic,
		},
	}
}

func (s *server) auditSandboxSuiteQueued(ctx context.Context, accountID string, run state.APISandboxSuiteRun) {
	s.audit.Emit(ctx, "api_sandbox.suite_run_queued", &accountID, map[string]any{
		"app_id": run.AppID, "run_id": run.ID, "candidate_deployment_id": run.CandidateDeploymentID,
		"suite_revision": run.SuiteRevision, "automatic": run.Automatic,
	})
}

func (s *server) writeAPISandboxSuiteRun(w http.ResponseWriter, r *http.Request, app state.App, candidate state.Deployment, checks state.APISandboxApprovalCheckStore,
	store state.APISandboxSuiteStore, run state.APISandboxSuiteRun, retry *api.APISandboxSuiteRunRequest, status int) {
	if run.AppID != app.ID || run.CandidateDeploymentID != candidate.ID {
		s.notFound(w, "no such sandbox suite run for this release")
		return
	}
	if retry != nil && (run.BaselineDeploymentID != retry.BaselineDeploymentID || run.SuiteRevision != retry.SuiteRevision) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox suite run already configured",
			"retry with the original baseline and suite revision or use a new run UUID"))
		return
	}
	report, err := s.sandboxSuiteReport(r, app, candidate, checks, store, run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox suite report"))
		return
	}
	writeJSON(w, status, report)
}

func (s *server) sandboxSuiteReport(r *http.Request, app state.App, candidate state.Deployment, checks state.APISandboxApprovalCheckStore,
	store state.APISandboxSuiteStore, run state.APISandboxSuiteRun) (api.APISandboxSuiteRunReport, error) {
	ctx := r.Context()
	in, err := s.sandboxApprovalInputs(ctx, app, candidate, true)
	if err != nil {
		return api.APISandboxSuiteRunReport{}, err
	}
	suite, err := store.GetAPISandboxSuite(ctx, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return api.APISandboxSuiteRunReport{}, err
	}
	items, err := store.ListAPISandboxSuiteScenarios(ctx, run.ID)
	if err != nil {
		return api.APISandboxSuiteRunReport{}, err
	}
	out := api.APISandboxSuiteRunReport{
		APISandboxSuiteRunSummary: run.APISandboxSuiteRunSummary, FailureCode: run.FailureCode,
		Verdict: api.APISandboxComparisonRunning, Scenarios: make([]api.APISandboxSuiteScenarioResult, 0, len(items)),
	}
	var passed int
	var regression bool
	for _, item := range items {
		result := item.APISandboxSuiteScenarioResult
		evidence, err := checks.GetAPISandboxApprovalCheck(ctx, item.ComparisonID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			result.Status = "queued"
			if run.FinishedAt != nil {
				result.Status = "not_run"
			}
		case err != nil:
			return api.APISandboxSuiteRunReport{}, err
		default:
			result.Status, result.Comparison = evidence.Comparison.Verdict, &evidence.Comparison
			if result.Status == api.APISandboxComparisonPassed {
				passed++
			}
			regression = regression || result.Status == api.APISandboxComparisonRegression
		}
		out.Scenarios = append(out.Scenarios, result)
	}
	if run.FinishedAt != nil {
		switch {
		case regression:
			out.Verdict = api.APISandboxComparisonRegression
		case run.FailureCode == "" && passed == run.ScenarioCount:
			out.Verdict = api.APISandboxComparisonPassed
		default:
			out.Verdict = api.APISandboxComparisonInconclusive
		}
	}
	out.StaleReason = sandboxSuiteStaleReason(in, run, suite.Revision, items)
	if out.StaleReason != "" {
		out.Verdict = api.APISandboxApprovalStale
	}
	return out, nil
}

func sandboxSuiteStaleReason(in sandboxApprovalInputs, run state.APISandboxSuiteRun, suiteRevision string, items []state.APISandboxSuiteScenario) string {
	switch {
	case in.pins.Candidate != run.Pins.Candidate:
		return "candidate_artifact_changed"
	case in.baselineID != run.BaselineDeploymentID || in.pins.Baseline != run.Pins.Baseline:
		return "live_baseline_changed"
	case suiteRevision != run.SuiteRevision:
		return "suite_changed"
	}
	for _, item := range items {
		if !slices.ContainsFunc(in.library, func(current api.APISandboxLibraryScenarioSummary) bool {
			return current.ID == item.ScenarioID && current.Revision == item.ScenarioRevision
		}) {
			return "suite_scenario_changed"
		}
	}
	return ""
}
