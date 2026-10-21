package rehearsal

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
)

func ValidateColdStart(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	if req.ColdStart == nil {
		return nil
	}
	if _, _, err := api.ScenarioColdStartEnrollment(req); err != nil {
		return api.ErrValidation(err.Error())
	}
	if !enabled {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Cold-start rehearsal unavailable", "The operator must qualify and enable contained cold-start rehearsals")
	}
	return nil
}
