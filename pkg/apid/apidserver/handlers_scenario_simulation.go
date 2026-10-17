// adr: 1350
package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state"
)

func scenarioSimulationEnrollment(req api.RegisterScenarioTestRequest) (string, string, error) {
	if req.Simulation == nil {
		return "", "", nil
	}
	names := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		names = append(names, m.Workload)
	}
	body, digest, err := api.NormalizeScenarioSimulationPlan(*req.Simulation, names)
	return string(body), digest, err
}

func (s *server) scenarioSimulationSupported(w http.ResponseWriter, req api.RegisterScenarioTestRequest) bool {
	if req.Simulation == nil {
		return true
	}
	if !s.rehearsalSimulationEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Rehearsal simulation unavailable", "The operator must qualify and enable rehearsal action simulation before enrolling fixtures"))
		return false
	}
	if req.NetworkMode != state.ScenarioNetworkContained || req.WebhookMode != state.ScenarioWebhookCaptureMode || req.ActionEvidenceVersion != api.ScenarioSimulationSnapshotVersion {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid simulation policy", "Simulation requires contained networking, webhook capture and action_evidence_version 2"))
		return false
	}
	if _, _, err := scenarioSimulationEnrollment(req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid simulation fixtures", err.Error()))
		return false
	}
	if _, ok := s.store.(state.ScenarioSimulationStore); !ok {
		api.WriteProblem(w, api.ErrCapacity("register simulation evidence support"))
		return false
	}
	if _, ok := s.store.(rehearsalrecorder.Store); !ok {
		api.WriteProblem(w, api.ErrCapacity("register durable recorder support"))
		return false
	}
	return true
}

func scenarioRecorderVersion(req api.RegisterScenarioTestRequest) int {
	if req.Simulation != nil {
		return rehearsalrecorder.Version
	}
	return 0
}

func scenarioSimulationEnrollmentDigest(req api.RegisterScenarioTestRequest) string {
	_, digest, _ := scenarioSimulationEnrollment(req)
	return digest
}
