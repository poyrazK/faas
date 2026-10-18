package rehearsal

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func ValidateSuspendResume(req api.RegisterScenarioTestRequest, enabled, runningEnabled bool) *api.Problem {
	if req.SuspendResume == nil {
		return nil
	}
	if _, _, err := api.ScenarioSuspendResumeEnrollment(req); err != nil {
		return api.ErrValidation(err.Error())
	}
	if !enabled {
		return api.ErrCapacity("The operator must qualify and enable contained suspend/resume rehearsals")
	}
	if req.SuspendResume.ManagedJob() && !runningEnabled {
		return api.ErrCapacity("The operator must qualify and enable running-job suspend/resume rehearsals")
	}
	return nil
}

// Durable intent handling is independent of the enrollment switch.
func SuspendResume(ctx context.Context, store any, account, run string, begin bool) (rehearsalresume.Evidence, error) {
	s, ok := store.(rehearsalresume.Store)
	if !ok {
		return rehearsalresume.Evidence{}, errors.New("suspend/resume receipt storage unavailable")
	}
	if begin {
		return s.BeginScenarioSuspendResume(ctx, account, run)
	}
	return s.ScenarioSuspendResumeEvidence(ctx, account, run)
}
