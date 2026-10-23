// adr: 20261023111825
package rehearsal

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateQueueDelivery admits same-run delivery of captured queue sends only
// for a contained run with no managed-work or suspend/resume plan, whose
// fences would cancel or block the delivered invocations, and only behind the
// operator flag.
func ValidateQueueDelivery(req api.RegisterScenarioTestRequest, enabled bool) *api.Problem {
	switch req.QueueDelivery {
	case "":
		return nil
	case api.ScenarioQueueDeliverySameRun:
	default:
		return api.ErrValidation("queue_delivery must be empty or same_run")
	}
	if req.NetworkMode != "contained" || req.ManagedWork != nil || req.SuspendResume != nil {
		return api.ErrValidation("queue_delivery same_run requires a contained run without managed_work or suspend_resume")
	}
	if !enabled {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Queue delivery unavailable",
			"The operator must qualify and enable rehearsal queue delivery before enrolling a delivering run")
	}
	return nil
}
