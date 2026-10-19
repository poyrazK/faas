package api

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func ScenarioManagedWorkEnrollment(req RegisterScenarioTestRequest) (string, string, error) {
	if req.ManagedWork == nil {
		return "", "", nil
	}
	if req.CommandVersion != 1 || req.Simulation == nil || req.NetworkMode != "contained" || req.WebhookMode != "capture" || req.ActionEvidenceVersion != ScenarioSimulationSnapshotVersion {
		return "", "", errors.New("managed_work requires contained commands, simulation and action evidence version 2")
	}
	names := make([]string, 0, len(req.Members))
	for _, m := range req.Members {
		names = append(names, m.Workload)
	}
	b, d, e := rehearsalwork.Normalize(*req.ManagedWork, names)
	if e == nil {
		e = ValidateScenarioWorkerTermination(*req.ManagedWork, req.Simulation)
	}
	if e == nil {
		e = ValidateScenarioPostgresFault(req)
	}
	return string(b), d, e
}

func ValidateScenarioPostgresFault(req RegisterScenarioTestRequest) error {
	if req.ManagedWork == nil || req.ManagedWork.PostgresFault == nil {
		return nil
	}
	f := req.ManagedWork.PostgresFault
	if req.SuspendResume != nil || req.ColdStart != nil {
		return errors.New("postgres_fault cannot combine cold start or suspend/resume")
	}
	if req.Simulation != nil {
		for _, fixture := range req.Simulation.Fixtures {
			if fixture.Workload != f.Workload || fixture.Name != f.Checkpoint {
				continue
			}
			if fixture.Adapter == "http" && fixture.Integration == "checkpoint" && fixture.Method == "POST" && len(fixture.Responses) == 1 {
				r := fixture.Responses[0]
				if r.Status == 200 && r.Fault == "" && r.DelayMS == 0 && r.RetryAfterSeconds == 0 {
					return nil
				}
			}
			return errors.New("postgres_fault requires a checkpoint POST HTTP fixture with one un-delayed HTTP 200 response")
		}
	}
	return errors.New("postgres_fault checkpoint simulation is missing")
}

func ValidateScenarioWorkerTermination(plan rehearsalwork.Plan, simulation *ScenarioSimulationPlan) error {
	f := plan.WorkerTermination
	if f == nil {
		return nil
	}
	if simulation != nil {
		for _, fixture := range simulation.Fixtures {
			if fixture.Workload != f.Workload || fixture.Name != f.Checkpoint {
				continue
			}
			if fixture.Adapter == "http" && fixture.Method == "POST" && len(fixture.Responses) == 1 {
				r := fixture.Responses[0]
				if r.Status == 200 && r.Fault == "" && r.DelayMS == 0 && r.RetryAfterSeconds == 0 {
					return nil
				}
			}
			return errors.New("worker termination checkpoint requires a POST HTTP fixture with one HTTP 200 response, without delay or fault")
		}
	}
	return errors.New("worker termination checkpoint fixture is missing")
}
func validateScenarioManagedWorkEnrollment(req RegisterScenarioTestRequest) (string, error) {
	_, d, e := ScenarioManagedWorkEnrollment(req)
	return d, e
}
