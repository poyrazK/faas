package api

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
)

func ScenarioColdStartEnrollment(req RegisterScenarioTestRequest) (string, string, error) {
	if req.ColdStart == nil {
		return "", "", nil
	}
	if req.CommandVersion != 1 || req.NetworkMode != "contained" || req.WebhookMode != "capture" || req.Simulation == nil || req.ActionEvidenceVersion != ScenarioSimulationSnapshotVersion || req.ManagedWork != nil && req.ManagedWork.WorkerTermination != nil {
		return "", "", errors.New("cold start requires contained commands and simulation without worker termination")
	}
	names := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		names = append(names, m.Workload)
	}
	raw, digest, err := rehearsalcold.Normalize(*req.ColdStart, names)
	return string(raw), digest, err
}
