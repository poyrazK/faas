package orgbilling

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func PlanResponse(owner state.Account, response api.AccountResponse) any {
	if owner.ResourceOrgID == "" {
		return response
	}
	return api.OrgPlanResponse{OrgID: owner.ResourceOrgID, Plan: owner.Plan,
		PlanChangeStatus: response.PlanChangeStatus, RequestedPlan: response.RequestedPlan, EffectiveAt: response.EffectiveAt}
}
