// adr: 20261019071733 amendment 1, 20261020085706 amendment 1 — GitOps
// activation into production meets the release guards.
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
//     the revision in Gregale (approval policy manual) and that one approval
//     meets the app's policy (manualRevisionApprovalHold). A protected-branch
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
		active, err := freeze.ActiveFor(ctx, b.server.store, app, scope, now)
		if err != nil {
			b.server.log.Warn("deploy freeze: could not read freezes; activating", "app", app.ID, "err", err)
		} else if len(active) > 0 {
			return "deploy freeze on " + app.Slug, nil
		}
		required, err := approval.CanaryRequired(ctx, b.server.store, app, scope, "")
		if err != nil {
			return "", err
		}
		if !required {
			continue
		}
		if lease.Source.Spec.ApprovalPolicy != gitOpsManualApproval {
			return "release approvals required by " + app.Slug, nil
		}
		if hold, err := b.manualRevisionApprovalHold(ctx, app, lease.Revision.ApprovedBy); err != nil || hold != "" {
			return hold, err
		}
	}
	return "", nil
}

// manualRevisionApprovalHold names why the person who approved a revision in
// Gregale does not meet app's release approval policy, or "" when they do
// (ADR-20261020085706 amendment 8). The revision carries one approval: a
// policy asking for more holds it, and on a company app the approver must
// hold a role the policy lets approve. The revision's author is a Git
// identity, not a Gregale account, so self-approval is not judged, as for a
// protected-branch revision.
func (b *environmentGitOpsBackend) manualRevisionApprovalHold(ctx context.Context, app state.App, approverAccountID string) (string, error) {
	store, ok := b.server.store.(state.ReleaseApprovalStore)
	if !ok {
		return "release approvals required by " + app.Slug, nil
	}
	policy, err := store.ReleaseApprovalPolicy(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// ADR-20261020085706 amendment 4: a key or token, as CI holds, does not
	// approve a release. A person approving the same revision in the
	// dashboard replaces its approval.
	if strings.HasPrefix(approverAccountID, state.EnvironmentRevisionKeyApproverPrefix) {
		return "release approvals required by " + app.Slug + ": an API key approved the revision; a person approves it on the environment's GitOps page", nil
	}
	if policy.RequiredApprovals > 1 {
		return fmt.Sprintf("%s requires %d release approvals; an approved revision carries one", app.Slug, policy.RequiredApprovals), nil
	}
	shared, err := b.server.appSharedInOrg(ctx, app)
	if err != nil || !shared {
		return "", err
	}
	member, err := b.server.store.OrgMemberByAccount(ctx, app.OrgID, approverAccountID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (member.RemovedAt != nil || !approval.RoleAllowed(policy.Approvers, member.Role)) {
		return "the revision's approver may not approve releases of " + app.Slug, nil
	}
	return "", err
}

// gitRevisionApprover records who approved a GitOps revision: the person,
// or the key or token that approved it, which activation into production
// does not count as a release approval (manualRevisionApprovalHold).
func gitRevisionApprover(r *http.Request, acct state.Account) string {
	if p, ok := principalFrom(r); ok && p.Key != nil {
		return state.EnvironmentRevisionKeyApproverPrefix + p.Key.ID
	}
	return actorAccount(r, acct).ID
}
