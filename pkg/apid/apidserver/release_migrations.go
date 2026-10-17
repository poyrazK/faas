package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/state"
)

// withRelease fills Release on a single-deployment read (ADR-1448). A read
// failure omits the field; it explains the deployment, it is not state a
// client must act on.
func (s *server) withRelease(ctx context.Context, resp api.DeploymentResponse, d state.Deployment) api.DeploymentResponse {
	summary, err := release.ReleaseSummary(ctx, s.store, d)
	if err != nil {
		s.log.Warn("apid: read deployment release", "deployment", d.ID, "err", err)
		return resp
	}
	resp.Release = summary
	return resp
}

// rollbackReleaseGuard refuses a rollback over release commands that ran
// after its target last served unless the caller acknowledged them
// (ADR-1448). It returns the crossed releases for the audit row.
func (s *server) rollbackReleaseGuard(ctx context.Context, app state.App, target state.Deployment, acknowledged bool) ([]state.ReleaseRun, *api.Problem) {
	runs, problem, err := release.RollbackGuard(ctx, s.store, app.ID, target, acknowledged)
	if err != nil {
		return nil, customerCapacityProblem(s.log, "check releases crossed by rollback", "Deployment temporarily unavailable",
			"Gregale could not check which release commands this rollback would cross.",
			"Retry the request in a moment; if it continues, contact support.", err)
	}
	return runs, problem
}

// crossedReleaseIDs lists the deployments whose release commands an
// acknowledged rollback crossed, for its audit row.
func crossedReleaseIDs(runs []state.ReleaseRun) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.DeploymentID)
	}
	return ids
}
