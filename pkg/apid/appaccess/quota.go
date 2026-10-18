package appaccess

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func AdmissionUnavailableProblem() *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeForbidden,
		"Cannot create or restore application", "the organization or account is inactive; restore it before creating or restoring applications")
}

// QuotaProblem uses the plan/count observed by the admission transaction. The
// caller's personal plan can differ from the organization's plan, and a plan
// change can commit after HTTP authorization but before admission.
func QuotaProblem(err *state.QuotaError, fallback api.Limits) *api.Problem {
	if err.Plan != "" {
		if authoritative, ok := api.LimitsFor(err.Plan); ok {
			fallback = authoritative
		}
	}
	switch err.Kind {
	case state.QuotaErrorKindDeveloperApps:
		fallback.DeveloperApps = err.Limit
		return api.ErrPlanLimitDeveloperApps(fallback, err.Observed)
	default:
		fallback.DeployedApps = err.Limit
		return api.ErrPlanLimitApps(fallback, err.Observed)
	}
}
