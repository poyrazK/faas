package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/trafficproblem"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeDeploymentCreateError keeps lifecycle failures distinct from actual
// capacity failures. CreateDeployment uses ErrNotFound when the app vanished
// or became terminal after the handler's initial lookup.
func (s *server) writeDeploymentCreateError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrDeploymentSubmissionConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDeploymentAttemptAccepted,
			"Deployment attempt already accepted", "recover the original deployment using its submission receipt; do not resubmit"))
		return
	}
	if errors.Is(err, state.ErrApplicationStandardsPending) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending, "Application standards pending", "Inherited application controls must be persisted before this service accepts a deployment."))
		return
	}
	if writeReleaseReviewError(w, err) {
		return
	}
	if p := routeRemovalBlockedProblem(err); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if state.IsBindingReleaseRequired(err) {
		api.WriteProblem(w, bindingReleaseRequiredProblem())
		return
	}
	if problem := api.AsProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "app no longer accepts deployments")
		return
	}
	fallback := api.ErrCapacity("could not create deployment")
	if problem := state.ServiceCapacityProblem(err); problem != nil {
		fallback = problem
	}
	// The client only sees "Briefly at capacity", and no caller logs the
	// cause, so without this line the failure is untraceable (hunt #9
	// H9-9/H9-63: two production deploys refused with nothing in the log).
	if s.log != nil {
		s.log.Error("create deployment failed", "err", err)
	}
	api.WriteProblem(w, trafficproblem.Policy(err, fallback))
}
