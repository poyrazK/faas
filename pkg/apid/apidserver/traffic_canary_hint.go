package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// trafficChangeDuringCanaryProblem refuses a traffic change while a canary
// runs and names that canary's next commands. A failed read falls back to the
// generic refusal: the refusal itself is already decided.
func (s *server) trafficChangeDuringCanaryProblem(ctx context.Context, app state.App) *api.Problem {
	live, err := s.store.LiveDeployments(ctx, app.ID)
	if err != nil {
		return api.ErrTrafficChangeDuringCanary()
	}
	for _, d := range live {
		if runningCanary(d) {
			return api.ErrTrafficChangeDuringCanaryFor(app.Slug, d.ID, d.CanaryStep)
		}
	}
	return api.ErrTrafficChangeDuringCanary()
}

func runningCanary(d state.Deployment) bool {
	return d.Status == state.DeployLive && d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps &&
		(d.RolloutState == "pending" || d.RolloutState == "rolling_out")
}

// recoverRolloutStepProblem checks the canary step a recovery caller
// observed. The check precedes the recovery transaction: it keeps a promote
// typed from a refusal's hint from finishing a canary that has since moved on
// or been replaced; the store still applies its own state checks.
func (s *server) recoverRolloutStepProblem(ctx context.Context, app state.App, expected int) *api.Problem {
	live, err := s.store.LiveDeployments(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the running canary")
	}
	var canary state.Deployment
	for _, d := range live {
		if runningCanary(d) && (canary.ID == "" || d.CreatedAt.After(canary.CreatedAt)) {
			canary = d
		}
	}
	if canary.ID == "" {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No running canary", "this app has no canary rollout in progress, so there is no step to check.")
	}
	if canary.CanaryStep != expected {
		return api.ErrCanaryStepConflict(expected, canary.CanaryStep)
	}
	return nil
}
