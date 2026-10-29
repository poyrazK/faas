package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rehearsal"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerScenarioTest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", "run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	var req api.RegisterScenarioTestRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if !s.scenarioSuspendResumeSupported(w, req) {
		return
	}
	if !s.scenarioColdStartSupported(w, req) {
		return
	}
	if !s.scenarioManagedWorkSupported(w, req) {
		return
	}
	if !s.scenarioTestPolicySupported(w, req) {
		return
	}
	members := s.resolveScenarioTestMembers(w, r, acct.ID, runID, req)
	if members == nil {
		return
	}
	if err := s.store.RegisterScenarioTestMembers(r.Context(), acct.ID, runID, members); err != nil {
		switch {
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Test environment conflict", "run or workload is already registered"))
		case errors.Is(err, state.ErrScenarioManagedBindings):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Managed bindings cannot be contained",
				"a contained rehearsal member cannot use real managed PostgreSQL or object storage; declare isolated postgres/objects fixtures instead"))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "test developer session is unavailable")
		default:
			api.WriteProblem(w, api.ErrCapacity("register test environment"))
		}
		return
	}
	if req.WebhookMode != "" {
		writeJSON(w, http.StatusOK, api.RegisterScenarioTestResponse{SuspendResumeSHA256: scenarioSuspendResumeDigest(req), ColdStartSHA256: scenarioColdStartDigest(req), ManagedWorkSHA256: scenarioManagedWorkDigest(req), CommandVersion: req.CommandVersion, WebhookMode: req.WebhookMode, PolicyVersion: 1, ActionEvidenceVersion: req.ActionEvidenceVersion, NetworkMode: req.NetworkMode, SimulationSHA256: scenarioSimulationEnrollmentDigest(req), RecorderVersion: scenarioRecorderVersion(req)})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) scenarioTestPolicySupported(w http.ResponseWriter, req api.RegisterScenarioTestRequest) bool {
	if (req.NetworkMode != "" && (req.NetworkMode != state.ScenarioNetworkContained || req.WebhookMode != state.ScenarioWebhookCaptureMode)) || len(req.Members) == 0 || len(req.Members) > 16 || req.WebhookMode != "" && req.WebhookMode != state.ScenarioWebhookCaptureMode ||
		req.ActionEvidenceVersion != 0 && ((req.ActionEvidenceVersion != api.ScenarioActionSnapshotVersion && req.ActionEvidenceVersion != api.ScenarioSimulationSnapshotVersion) || req.WebhookMode != state.ScenarioWebhookCaptureMode) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid test policy", "members must contain 1 to 16 workloads; capture supports action_evidence_version 1 and optional network_mode contained"))
		return false
	}
	if req.NetworkMode != "" && !s.rehearsalNetworkEnabled {
		api.WriteProblem(w, api.ErrRehearsalNotEnabled("Rehearsal network containment"))
		return false
	}
	if problem := rehearsal.ValidateEnrollment(req, s.rehearsalCommandsEnabled && s.appTaskAPIEnabled); problem != nil {
		api.WriteProblem(w, problem)
		return false
	}
	if req.CommandVersion != 0 {
		if _, ok := s.store.(state.ScenarioCommandStore); !ok {
			api.WriteProblem(w, api.ErrCapacity("register contained command support"))
			return false
		}
	}
	if !s.scenarioSimulationSupported(w, req) {
		return false
	}
	if req.ActionEvidenceVersion == api.ScenarioSimulationSnapshotVersion && req.Simulation == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Missing simulation fixtures", "Action evidence version 2 requires simulation fixtures"))
		return false
	}
	if req.ActionEvidenceVersion != 0 {
		if _, ok := s.store.(state.ScenarioActionSnapshotStore); !ok {
			api.WriteProblem(w, api.ErrCapacity("register action evidence support"))
			return false
		}
	}
	return true
}

func (s *server) resolveScenarioTestMembers(w http.ResponseWriter, r *http.Request, accountID, runID string, req api.RegisterScenarioTestRequest) []state.ScenarioTestMember {
	simulationJSON, _, _ := scenarioSimulationEnrollment(req)
	workJSON, _ := scenarioManagedWorkEnrollment(req)
	coldJSON, _, _ := api.ScenarioColdStartEnrollment(req)
	resumeJSON, _, _ := api.ScenarioSuspendResumeEnrollment(req)
	// A plain namespace (`gregale dev --all`) names members by any service
	// name; rehearsal enrollments keep slugs (ADR-20261026105459).
	plain := req.WebhookMode == "" && req.NetworkMode == "" && simulationJSON == "" && workJSON == "" && coldJSON == "" && resumeJSON == ""
	members := make([]state.ScenarioTestMember, 0, len(req.Members))
	for _, spec := range req.Members {
		if (plain && !api.ValidServiceName(spec.Workload)) || (!plain && !validSlug(spec.Workload)) || !validSlug(spec.AppSlug) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid workload", "workload and app_slug must be valid slugs"))
			return nil
		}
		app, err := s.store.AppBySlug(r.Context(), spec.AppSlug)
		if err != nil || app.AccountID != accountID || app.PreviewPrNumber != 0 || app.PreviewOfSlug == "" ||
			devSessionSlug(accountID, app.PreviewOfSlug, runID) != app.Slug {
			s.notFound(w, "no such test developer session")
			return nil
		}
		members = append(members, state.ScenarioTestMember{Workload: spec.Workload, AppID: app.ID, WebhookMode: req.WebhookMode, NetworkMode: req.NetworkMode, ColdStartJSON: coldJSON, SuspendResumeJSON: resumeJSON, SimulationJSON: simulationJSON, ManagedWorkJSON: workJSON})
	}
	return members
}

func (s *server) deleteScenarioTest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		s.notFound(w, "no such test environment")
		return
	}
	if err := s.store.DeleteScenarioTestMembers(r.Context(), acct.ID, runID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Test environment is active", "destroy its developer sessions before deleting the environment"))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such test environment")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("delete test environment"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) injectScenarioTestChaos(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", "run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	var req api.InjectScenarioTestChaosRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	rules := make([]chaos.Rule, len(req.Rules))
	for i, rule := range req.Rules {
		rules[i] = chaos.Rule(rule)
	}
	lease, err := s.store.SetScenarioTestChaosPlan(r.Context(), acct.ID, runID, chaos.Plan{DurationMS: req.DurationMS, Rules: rules})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid chaos plan", err.Error()))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no live scenario test run")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("install scenario chaos plan"))
		return
	}
	writeJSON(w, http.StatusOK, api.InjectScenarioTestChaosResponse{
		ExpiresAt: lease.ExpiresAt, RulesInstalled: len(lease.Rules), Generation: lease.Generation,
	})
}

func (s *server) getScenarioTestChaosMatches(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", "run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	evidence, err := s.store.ScenarioTestChaosMatchEvidence(r.Context(), acct.ID, runID)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", err.Error()))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no live scenario test run")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("read scenario chaos matches"))
		return
	}
	matches := make([]api.ScenarioTestChaosMatch, len(evidence.Matches))
	for i, match := range evidence.Matches {
		matches[i] = api.ScenarioTestChaosMatch{RuleID: match.RuleID, Count: match.Count}
	}
	writeJSON(w, http.StatusOK, api.ScenarioTestChaosMatchesResponse{Generation: evidence.Generation, Matches: matches})
}

func (s *server) clearScenarioTestChaos(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", "run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	if err := s.store.ClearScenarioTestChaosPlan(r.Context(), acct.ID, runID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", err.Error()))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no live scenario test run")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("clear scenario chaos plan"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
