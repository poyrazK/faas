package api

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func ScenarioSuspendResumeEnrollment(req RegisterScenarioTestRequest) (string, string, error) {
	if req.SuspendResume == nil {
		return "", "", nil
	}
	if req.CommandVersion != 1 || req.NetworkMode != "contained" || req.WebhookMode != "capture" || req.Simulation == nil || req.ActionEvidenceVersion != ScenarioSimulationSnapshotVersion || req.ColdStart != nil || req.ManagedWork != nil && req.ManagedWork.WorkerTermination != nil {
		return "", "", errors.New("suspend/resume requires contained commands and simulation without cold start or worker termination")
	}
	names := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		names = append(names, m.Workload)
	}
	raw, digest, err := rehearsalresume.Normalize(*req.SuspendResume, names)
	if err == nil {
		err = ValidateScenarioSuspendCheckpoint(*req.SuspendResume, req.ManagedWork, req.Simulation)
	}
	return string(raw), digest, err
}

// BeginScenarioSuspendResume records intent only; receipts are polled separately.
func (c *Client) BeginScenarioSuspendResume(ctx context.Context, run string) (rehearsalresume.Evidence, error) {
	var out rehearsalresume.Evidence
	err := c.do(ctx, "POST", "/v1/dev/test-runs/"+run+"/suspend-resume", nil, &out)
	return out, err
}
func (c *Client) GetScenarioSuspendResume(ctx context.Context, run string) (rehearsalresume.Evidence, error) {
	var out rehearsalresume.Evidence
	err := c.do(ctx, "GET", "/v1/dev/test-runs/"+run+"/suspend-resume", nil, &out)
	return out, err
}

// ValidateScenarioSuspendCheckpoint binds the application boundary to one async
// participant and an unambiguous successful simulation fixture.
func ValidateScenarioSuspendCheckpoint(spec rehearsalresume.Spec, plan *rehearsalwork.Plan, simulation *ScenarioSimulationPlan) error {
	if !spec.ManagedJob() {
		return nil
	}
	if plan == nil || len(spec.Workloads) != 1 || plan.WorkerTermination != nil {
		return errors.New("managed_job suspend requires managed work without worker termination")
	}
	workers := 0
	for _, p := range plan.Participants {
		if p.Async {
			workers++
			if p.Workload != spec.Workloads[0] {
				return errors.New("managed_job suspend supports exactly one async worker")
			}
		}
	}
	if workers != 1 {
		return errors.New("managed_job suspend needs one declared async worker")
	}
	checkpoint := *plan
	checkpoint.WorkerTermination = &rehearsalwork.WorkerTermination{Workload: spec.Workloads[0], Checkpoint: spec.WorkerCheckpoint}
	return ValidateScenarioWorkerTermination(checkpoint, simulation)
}
