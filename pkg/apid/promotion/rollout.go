package promotion

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
	"github.com/onebox-faas/faas/pkg/state"
)

// RolloutProblem only guards new verified admissions. Public responses omit
// approval paths, trust details and the identities of other rollout targets.
func RolloutProblem(gate *promotionrollout.Gate, account, project string, policy *api.ProjectPromotionHealthPolicy, capture *state.ProjectEnvironmentPromotionReleaseConfiguration) *api.Problem {
	if policy == nil {
		return nil
	}
	required := []promotionrollout.Capability{promotionrollout.Health}
	if policy.Migration != nil {
		required = append(required, promotionrollout.Migrations)
	}
	if len(api.ProjectPromotionWorkers(policy)) > 0 {
		required = append(required, promotionrollout.Workers)
	}
	if len(api.ProjectPromotionWorkers(policy)) > 1 {
		required = append(required, promotionrollout.WorkerSet)
	}
	if policy.Migration != nil && len(api.ProjectPromotionWorkers(policy)) > 0 {
		required = append(required, promotionrollout.Coordinated)
	}
	if capture != nil && capture.Secrets != nil {
		required = append(required, promotionrollout.Secrets)
	}
	if capture != nil && capture.DestinationConfigHash != "" {
		required = append(required, promotionrollout.ServiceGraph)
	}
	if gate.Check(promotionrollout.Target{AccountID: account, ProjectID: project}, required...) {
		return nil
	}
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Verified promotion unavailable", "New verified promotions are paused or unavailable for this project. Existing promotions and recovery remain available.")
}
