package githubd

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// holdPushOverDeployRate holds every project app of a push whose services do
// not all fit the account's remaining hourly deploys (ADR-20261105095300).
// Each service takes one deploy at the bridge; admitting them one by one let
// the budget run out part-way and left the project on mixed releases, with a
// durable delivery retrying the rest after the window reset. A held app gets a
// failed check and no deployment, and the delivery completes. The budget is
// advisory here: a store without the rate or a failed read leaves the bridge's
// per-app admission as the only gate.
func (s *Service) holdPushOverDeployRate(ctx context.Context, project state.Project, apps []state.App, held map[string]string) {
	need := 0
	for _, app := range apps {
		if _, ok := held[app.ID]; !ok {
			need++
		}
	}
	if need <= 1 || s.Reconcile == nil {
		return
	}
	rates, ok := s.Reconcile.Store.(state.AccountDeployRateStore)
	if !ok {
		return
	}
	acct, err := state.ProjectResourceAccount(ctx, s.Reconcile.Store, project)
	if err != nil {
		s.Log.Warn("githubd: deploy rate owner", "project_id", project.ID, "err", err)
		return
	}
	rate, err := rates.ReadAccountDeployRate(ctx, acct.ResourceOwnerID(), acct.Plan.DeploysPerHour(), time.Now().UTC())
	if err != nil {
		s.Log.Warn("githubd: read deploy rate", "project_id", project.ID, "err", err)
		return
	}
	if rate.Remaining >= need {
		return
	}
	reason := fmt.Sprintf("Not deployed: this push needs %d deploys for its services and the account has %d of %d left this hour (resets at %s). No service was deployed, so the project stays on one release. Push again or run `gregale deploy` after the reset.",
		need, rate.Remaining, rate.Limit, rate.WindowResetsAt.UTC().Format(time.RFC3339))
	for _, app := range apps {
		if _, ok := held[app.ID]; !ok {
			held[app.ID] = reason
		}
	}
	s.Log.Info("githubd: push over deploy rate", "project_id", project.ID, "need", need, "remaining", rate.Remaining)
}
