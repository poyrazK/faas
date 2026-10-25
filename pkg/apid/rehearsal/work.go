package rehearsal

import "github.com/onebox-faas/faas/pkg/api"

func ValidateManagedWork(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.ManagedWork == nil {
		return nil
	}
	if !enabled {
		return api.ErrRehearsalNotEnabled("Managed-worker rehearsal")
	}
	if _, _, e := api.ScenarioManagedWorkEnrollment(req); e != nil {
		return api.ErrValidation(e.Error())
	}
	return nil
}

func ValidateWorkerTermination(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.ManagedWork != nil && req.ManagedWork.WorkerTermination != nil && !enabled {
		return api.ErrRehearsalNotEnabled("Worker termination rehearsal")
	}
	return nil
}

func ValidatePostgresFault(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.ManagedWork != nil && req.ManagedWork.PostgresFault != nil && !enabled {
		return api.ErrRehearsalNotEnabled("PostgreSQL fault rehearsal")
	}
	return nil
}
