package rehearsal

import (
	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateColdStart(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.ColdStart == nil {
		return nil
	}
	if _, _, err := api.ScenarioColdStartEnrollment(req); err != nil {
		return api.ErrValidation(err.Error())
	}
	if !enabled {
		return api.ErrRehearsalNotEnabled("Contained cold-start rehearsal")
	}
	return nil
}
