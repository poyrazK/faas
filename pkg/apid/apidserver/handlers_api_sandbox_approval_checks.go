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

type sandboxApprovalInputs struct {
	availability string
	baselineID   string
	scenario     *api.APISandboxScenario
	library      []api.APISandboxLibraryScenarioSummary
	pins         state.APISandboxApprovalPins
}

func (s *server) sandboxApprovalInputs(ctx context.Context, app state.App, candidate state.Deployment, hasEvidence bool) (sandboxApprovalInputs, error) {
	in := sandboxApprovalInputs{availability: api.APISandboxApprovalNotWaiting}
	if candidate.DeletedAt == nil {
		in.pins.Candidate = state.APISandboxArtifactPin(candidate)
	}
	now := time.Now().UTC()
	wait, err := approval.LoadReleaseCommandWait(ctx, s.store, app, candidate, now)
	if err != nil {
		return in, err
	}
	if wait.Pending() || wait.SandboxSuiteWindowOpen(candidate, now) {
		in.availability = api.APISandboxApprovalUnsupported
		if s.apiSandboxWorkerEnabled() && app.Status == state.AppActive && app.PreviewOfSlug == "" &&
			apiSandboxHTTPSourceApp(app) && state.APISandboxApprovalCandidateSupported(candidate) && in.pins.Candidate != "" {
			_, err := s.store.ReleaseAppTaskByDeployment(ctx, candidate.ID)
			switch {
			case errors.Is(err, state.ErrNotFound):
				in.availability = api.APISandboxApprovalReady
			case err != nil:
				return in, err
			}
		}
	}
	if in.availability != api.APISandboxApprovalReady && !hasEvidence {
		return in, nil
	}
	baseline, err := s.sandboxApprovalBaseline(ctx, app.ID, candidate.ID)
	if errors.Is(err, state.ErrNotFound) {
		if in.availability == api.APISandboxApprovalReady {
			in.availability = api.APISandboxApprovalNoBaseline
		}
		return in, nil
	}
	if err != nil {
		return in, err
	}
	in.baselineID, in.pins.Baseline = baseline.ID, state.APISandboxArtifactPin(baseline)
	in.scenario, err = s.savedAPISandboxScenario(ctx, baseline.ID)
	if err != nil {
		return in, err
	}
	if library, ok := s.store.(state.APISandboxScenarioLibraryStore); ok {
		in.library, err = library.ListAPISandboxLibraryScenarios(ctx, app.ID)
	}
	if in.availability == api.APISandboxApprovalReady && in.scenario == nil && len(in.library) == 0 {
		in.availability = api.APISandboxApprovalNoScenario
	}
	return in, err
}

func (in sandboxApprovalInputs) includesRevision(revision string) bool {
	return (in.scenario != nil && in.scenario.Revision == revision) ||
		slices.ContainsFunc(in.library, func(entry api.APISandboxLibraryScenarioSummary) bool {
			return entry.Revision == revision
		})
}

func (s *server) sandboxApprovalBaseline(ctx context.Context, appID, candidateID string) (state.Deployment, error) {
	live, err := s.store.LiveDeployments(ctx, appID)
	if err != nil {
		return state.Deployment{}, err
	}
	baseline, err := state.APISandboxApprovalBaseline(live, candidateID)
	if err != nil {
		return state.Deployment{}, err
	}
	serving, err := state.ResolveProductionDeployment(ctx, s.store, appID)
	if err != nil {
		return state.Deployment{}, err
	}
	if serving.ID != baseline.ID {
		return state.Deployment{}, state.ErrNotFound
	}
	return baseline, nil
}

func sandboxApprovalReceipt(evidence state.APISandboxApprovalEvidence, in sandboxApprovalInputs) api.APISandboxApprovalCheck {
	report := evidence.Comparison
	out := api.APISandboxApprovalCheck{Comparison: report, Verdict: report.Verdict}
	switch {
	case in.pins.Candidate == "" || evidence.Pins.Candidate != in.pins.Candidate:
		out.StaleReason = "candidate_artifact_changed"
	case report.BaselineDeploymentID != in.baselineID || evidence.Pins.Baseline != in.pins.Baseline:
		out.StaleReason = "live_baseline_changed"
	case !in.includesRevision(report.ScenarioRevision):
		out.StaleReason = "baseline_scenario_changed"
	}
	if out.StaleReason != "" {
		out.Verdict = api.APISandboxApprovalStale
	}
	return out
}

func (s *server) sandboxApprovalRelease(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Deployment, state.App, state.APISandboxApprovalCheckStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if apiSandboxPathID(r, "id") == "" {
		s.notFound(w, "no such deployment")
		return state.Deployment{}, state.App{}, nil, false
	}
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return d, app, nil, false
	}
	store, ok := s.store.(state.APISandboxApprovalCheckStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox approval checks are unavailable"))
	}
	return d, app, store, ok
}

func (s *server) listSandboxApprovalChecks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, store, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	ctx := r.Context()
	evidence, err := store.ListAPISandboxApprovalChecks(ctx, d.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list sandbox approval checks"))
		return
	}
	in, err := s.sandboxApprovalInputs(ctx, app, d, len(evidence) != 0)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read current sandbox approval inputs"))
		return
	}
	out := api.APISandboxApprovalChecksResponse{
		Availability: in.availability, BaselineDeploymentID: in.baselineID,
		Checks:           make([]api.APISandboxApprovalCheck, 0, len(evidence)),
		LibraryScenarios: in.library,
	}
	if in.scenario != nil {
		out.ScenarioRevision, out.ScenarioName = in.scenario.Revision, in.scenario.Name
	}
	for _, entry := range evidence {
		out.Checks = append(out.Checks, sandboxApprovalReceipt(entry, in))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getSandboxApprovalCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, store, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "check")
	if id == "" {
		s.notFound(w, "no such sandbox approval check")
		return
	}
	evidence, err := store.GetAPISandboxApprovalCheck(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such sandbox approval check")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the sandbox approval check"))
		return
	}
	s.writeSandboxApprovalCheck(w, r, d, app, evidence, nil, http.StatusOK)
}

func (s *server) createSandboxApprovalCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, store, ok := s.sandboxApprovalRelease(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "check")
	var input api.APISandboxComparisonRequest
	if id == "" || decodeJSONSized(r, &input, 4096) != nil ||
		!canonicalSandboxComparisonID(input.BaselineDeploymentID) || !canonicalSandboxComparisonID(input.ScenarioRevision) {
		api.WriteProblem(w, api.ErrValidation("use lowercase UUIDs for the check, its live baseline, and the saved scenario revision"))
		return
	}
	ctx := r.Context()
	existing, err := store.GetAPISandboxApprovalCheck(ctx, id)
	if err == nil {
		s.writeSandboxApprovalCheck(w, r, d, app, existing, &input, http.StatusOK)
		return
	}
	if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check an existing sandbox approval run"))
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, app) {
		return
	}
	in, err := s.sandboxApprovalInputs(ctx, app, d, false)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read current sandbox approval inputs"))
		return
	}
	if in.availability != api.APISandboxApprovalReady ||
		in.baselineID != input.BaselineDeploymentID || !in.includesRevision(input.ScenarioRevision) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox approval check is unavailable",
			"refresh the approval check and use the current held release, live baseline, and saved scenario"))
		return
	}
	scenario, err := s.selectedAPISandboxScenario(ctx, app.ID, in.baselineID, input.ScenarioRevision)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the selected scenario"))
		return
	}
	if scenario == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox scenario changed",
			"reload the scenario selection before starting the check"))
		return
	}
	apps, problem := s.buildAPISandboxComparisonApps(acct, app, in.baselineID, d.ID, scenario)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := store.CreateAPISandboxApprovalCheck(ctx, id, apps[0], apps[1], api.MustLimitsFor(acct.Plan), in.pins)
	if err != nil {
		if existing, readErr := store.GetAPISandboxApprovalCheck(ctx, id); readErr == nil {
			s.writeSandboxApprovalCheck(w, r, d, app, existing, &input, http.StatusOK)
			return
		}
		if !writeAPISandboxQuota(w, acct, err) {
			api.WriteProblem(w, api.ErrCapacity("could not reserve both sandbox approval runs"))
		}
		return
	}
	for _, sandbox := range created {
		s.emitAppCreated(ctx, sandbox)
	}
	s.audit.Emit(ctx, "api_sandbox.approval_check_created", &acct.ID, map[string]any{
		"comparison_id": id, "candidate_deployment_id": d.ID, "baseline_deployment_id": in.baselineID,
	})
	report, err := store.GetAPISandboxApprovalCheck(ctx, id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the queued sandbox approval check"))
		return
	}
	writeJSON(w, http.StatusAccepted, sandboxApprovalReceipt(report, in))
}

func (s *server) writeSandboxApprovalCheck(w http.ResponseWriter, r *http.Request, d state.Deployment, app state.App, evidence state.APISandboxApprovalEvidence, input *api.APISandboxComparisonRequest, status int) {
	report := evidence.Comparison
	if report.CandidateDeploymentID != d.ID {
		s.notFound(w, "no such sandbox approval check for this release")
		return
	}
	if input != nil && (report.BaselineDeploymentID != input.BaselineDeploymentID || report.ScenarioRevision != input.ScenarioRevision) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Sandbox approval check already configured",
			"retry with the original baseline and scenario revision or use a new check ID"))
		return
	}
	in, err := s.sandboxApprovalInputs(r.Context(), app, d, true)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify sandbox approval evidence"))
		return
	}
	writeJSON(w, status, sandboxApprovalReceipt(evidence, in))
}
