// adr: 20261019071733 amendment 1, 20261020085706 amendment 1 — GitOps
// activation into production meets the release guards.
package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/state"
)

// gitOpsManualApproval is the GitOps approval policy under which a person
// approves each revision inside Gregale.
const gitOpsManualApproval = "manual"

// gitOpsReleaseHold names what holds activating a prepared graph in a
// production environment, or "" when nothing does. Activation releases every
// member at once, so like the break-glass pause a hold leaves the sync
// unconverged and it activates once the hold ends:
//   - a deploy freeze covering a member app; an approved revision is not an
//     override, which stays recorded on deploys;
//   - a member app that requires release approvals, unless a person approved
//     the revision in Gregale (approval policy manual). A protected-branch
//     revision was approved outside Gregale, and activation has no canary
//     to wait in.
func (b *environmentGitOpsBackend) gitOpsReleaseHold(ctx context.Context, lease state.EnvironmentGitOpsLease, graph state.EnvironmentWorkloadGraph) (string, error) {
	scope := lease.Source.EnvironmentSlug
	if !freeze.IsProductionScope(scope) {
		return "", nil
	}
	now := time.Now().UTC()
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		app, err := b.server.store.AppByID(ctx, member.AppID)
		if err != nil {
			return "", err
		}
		active, err := freeze.ActiveFor(ctx, b.server.store, app.AccountID, app.ID, scope, now)
		if err != nil {
			b.server.log.Warn("deploy freeze: could not read freezes; activating", "app", app.ID, "err", err)
		} else if len(active) > 0 {
			return "deploy freeze on " + app.Slug, nil
		}
		if lease.Source.Spec.ApprovalPolicy == gitOpsManualApproval {
			continue
		}
		required, err := approval.CanaryRequired(ctx, b.server.store, app, scope, "")
		if err != nil {
			return "", err
		}
		if required {
			return "release approvals required by " + app.Slug, nil
		}
	}
	return "", nil
}
