// adr: 20261020085706 — amendment 12: a release that waits for approval
// waits before its release command.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/apid/freeze"
	"github.com/onebox-faas/faas/pkg/state"
)

// holdReleaseCommandForApproval reports whether dep's release command must
// not run yet because the app's approval policy gates the release. A release
// command (a migration) changes production data as soon as it runs, and the
// release cannot be undone by aborting its canary afterwards, so the approval
// is taken before the command, not only before traffic. A rejected release, or
// one that waited past the policy's timeout, fails here without running its
// command; an approved one runs it, and its canary does not wait again.
//
// A held release is resumed by the release-approval sweep
// (Loop.resumeReleaseApprovalHolds) through handleSnapshotBoot.
func (h *Handler) holdReleaseCommandForApproval(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	if _, err := h.store.ReleaseAppTaskByDeployment(ctx, dep.ID); err == nil {
		return false, nil // admitted already; its outcome owns the handoff
	} else if !errors.Is(err, state.ErrNotFound) {
		return true, fmt.Errorf("imaged: load deployment release task: %w", err)
	}
	wait, err := approval.LoadReleaseCommandWait(ctx, h.store, app, dep, time.Now())
	if err != nil {
		return true, fmt.Errorf("imaged: read release approval: %w", err)
	}
	if !wait.Waits {
		return false, nil
	}
	if ended, err := h.releaseScheduleEnded(ctx, dep); err != nil {
		return true, err
	} else if ended != "" {
		// The schedule said when this release should ship; it ended while
		// the release waited, so its release command must not run at some
		// later, unplanned time (ADR-20261020085706 amendment 14).
		return true, h.failReleaseApproval(ctx, app, dep, wait, api.CodeReleaseApprovalRequired,
			"the release's schedule ended ("+ended+") while it waited for approval; the release command did not run. Deploy and schedule it again")
	}
	switch wait.Status.State {
	case api.ReleaseApprovalStateApproved:
		return h.holdApprovedRelease(ctx, app, dep, wait)
	case api.ReleaseApprovalStateNotRequired:
		return false, nil
	case api.ReleaseApprovalStateRejected:
		return true, h.failReleaseApproval(ctx, app, dep, wait, api.CodeReleaseApprovalRejected,
			"the release was rejected before its release command ran; the command did not run and the serving release keeps its traffic")
	case api.ReleaseApprovalStateExpired:
		h.noticeReleaseApproval(ctx, app, dep, wait, state.ReleaseApprovalEventExpired)
		return true, h.failReleaseApproval(ctx, app, dep, wait, api.CodeReleaseApprovalRequired,
			"the release waited past its approval timeout before its release command ran; the command did not run. Deploy again to request approval again")
	}
	h.noticeReleaseApproval(ctx, app, dep, wait, state.ReleaseApprovalEventRequested)
	h.log.Info("imaged: release command waits for approval",
		"deployment_id", dep.ID, "app_id", app.ID, "approvals", wait.Status.Approvals,
		"required", wait.Status.RequiredApprovals, "expires_at", wait.Status.ExpiresAt)
	return true, nil
}

// holdApprovedRelease rechecks, as a scheduled release does when it starts,
// what may have changed while an approved release waited (ADR-20261020085706
// amendment 13). The break-glass pause holds it until lifted: its release
// command runs with production secrets. A deploy freeze that was not in force
// when it was deployed (and so was not overridden then) stops it before its
// command runs.
func (h *Handler) holdApprovedRelease(ctx context.Context, app state.App, dep state.Deployment, wait approval.ReleaseCommandWait) (bool, error) {
	if h.releasePauseOn() {
		h.log.Info("imaged: approved release waits for the break-glass release pause", "deployment_id", dep.ID, "app_id", app.ID)
		return true, nil
	}
	now := time.Now().UTC()
	active, err := freeze.ActiveFor(ctx, h.store, app, dep.Scope, now)
	if err != nil {
		return true, fmt.Errorf("imaged: read deploy freezes: %w", err)
	}
	if len(active) == 0 {
		return false, nil
	}
	atDeploy, err := freeze.ActiveFor(ctx, h.store, app, dep.Scope, dep.CreatedAt)
	if err != nil {
		return true, fmt.Errorf("imaged: read deploy freezes at deploy time: %w", err)
	}
	blocking := newFreezes(active, atDeploy, dep.CreatedAt)
	if len(blocking) == 0 {
		return false, nil
	}
	return true, h.failReleaseApproval(ctx, app, dep, wait, api.CodeReleaseFrozen,
		"a deploy freeze started while the release waited for approval ("+freeze.Summary(app.Slug, blocking)+
			"); the release command did not run. Deploy again when the freeze ends, or with --override-freeze REASON")
}

// releaseScheduleEnded names the outcome of dep's schedule when it ended
// without starting the release, or "" when dep has no schedule or it is
// still pending.
func (h *Handler) releaseScheduleEnded(ctx context.Context, dep state.Deployment) (string, error) {
	schedules, ok := h.store.(interface {
		LatestScheduledRelease(ctx context.Context, deploymentID string) (state.ScheduledRelease, error)
	})
	if !ok {
		return "", nil
	}
	sr, err := schedules.LatestScheduledRelease(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("imaged: read release schedule: %w", err)
	}
	if sr.State == state.ScheduledReleasePending || sr.State == state.ScheduledReleaseStarted {
		return "", nil
	}
	return sr.State, nil
}

// newFreezes are the active freezes that were not in force when the release
// was deployed: created since, or whose window started since.
func newFreezes(active, atDeploy []state.ReleaseFreeze, deployedAt time.Time) []state.ReleaseFreeze {
	before := make(map[string]bool, len(atDeploy))
	for _, f := range atDeploy {
		if !f.CreatedAt.After(deployedAt) {
			before[f.ID] = true
		}
	}
	var out []state.ReleaseFreeze
	for _, f := range active {
		if !before[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// supersedeOutpacedRelease retires dep before its release command runs when
// a newer revision of its app and scope exists (ADR-20261020085706 amendment
// 13): the promotion fence would supersede it at go-live anyway, after its
// older migration had run against the newer release's schema.
func (h *Handler) supersedeOutpacedRelease(ctx context.Context, dep state.Deployment) (bool, error) {
	store, ok := h.store.(state.OutpacedReleaseStore)
	if !ok {
		return false, nil
	}
	superseded, err := store.SupersedeOutpacedRelease(ctx, dep.ID)
	if err != nil {
		return true, fmt.Errorf("imaged: supersede outpaced release: %w", err)
	}
	if superseded {
		h.log.Info("imaged: release outpaced before its release command; superseded", "deployment_id", dep.ID, "app_id", dep.AppID)
		h.notifyDeploymentState(ctx, dep.AppID, dep.ID, state.DeploySuperseded)
	}
	return superseded, nil
}

func (h *Handler) failReleaseApproval(ctx context.Context, app state.App, dep state.Deployment, wait approval.ReleaseCommandWait, code, detail string) error {
	failed, err := h.store.SetDeploymentFailed(ctx, dep.ID, code, detail)
	if err != nil {
		return fmt.Errorf("imaged: fail deployment held for approval: %w", err)
	}
	h.log.Info("imaged: release closed before its release command",
		"deployment_id", dep.ID, "app_id", app.ID, "approval", wait.Status.State)
	h.notifyDeploymentState(ctx, failed.AppID, failed.ID, state.DeployFailed)
	return nil
}

// noticeReleaseApproval records an approval event for the policy's channels;
// the outbox records each event once per release, so a held release asks
// once however often it is checked. A failure is logged: a notice never
// decides the hold.
func (h *Handler) noticeReleaseApproval(ctx context.Context, app state.App, dep state.Deployment, wait approval.ReleaseCommandWait, event string) {
	if wait.Policy == nil || len(wait.Policy.NotifyChannelIDs) == 0 {
		return
	}
	events, ok := h.store.(state.ReleaseApprovalEventStore)
	if !ok {
		return
	}
	payload, err := json.Marshal(approval.Notice(event, app, dep, wait.Status, "", "", ""))
	if err != nil {
		h.log.Warn("imaged: encode release approval notice", "deployment_id", dep.ID, "err", err)
		return
	}
	if _, err := events.EnqueueReleaseApprovalEvent(ctx, state.ReleaseApprovalEvent{
		AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, Event: event,
		ChannelIDs: wait.Policy.NotifyChannelIDs, Payload: payload,
	}, time.Now()); err != nil {
		h.log.Warn("imaged: record release approval event", "deployment_id", dep.ID, "event", event, "err", err)
	}
}

// resumeReleaseApprovalHold re-enters the release handoff of a deployment
// whose image preparation is complete and whose release command has not been
// admitted: a release held for approval. Anything else is left alone.
func (h *Handler) resumeReleaseApprovalHold(ctx context.Context, dep state.Deployment) error {
	if dep.Status != state.DeploySnapshotting || len(dep.ReleaseCommand) == 0 || dep.EnvironmentWorkloadHeld() {
		return nil
	}
	if _, err := h.store.ReleaseAppTaskByDeployment(ctx, dep.ID); err == nil {
		return nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("imaged: load deployment release task: %w", err)
	}
	app, err := state.AppForDeployment(ctx, h.store, dep)
	if err != nil {
		return err
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil {
		return err
	}
	return h.handoffSnapshotPrime(ctx, app, dep)
}

// releaseApprovalHoldSweepEvery is how often imaged resumes releases held for
// approval. A decision is recorded by apid; the next sweep runs the release
// command of an approved release or closes a rejected or expired one.
const releaseApprovalHoldSweepEvery = 15 * time.Second

// resumeReleaseApprovalHolds hands each snapshotting deployment whose release
// command has not been admitted back to the image handoff, which holds it
// again, runs its command, or closes it. A preparation still in flight
// resumes through its own checkpoint instead (handleSnapshotBoot), so its scan
// freshness gate still applies.
func (l *Loop) resumeReleaseApprovalHolds(ctx context.Context) {
	if l.handler == nil || l.store == nil {
		return
	}
	if _, ok := l.store.(state.DeploymentImagePreparationStore); !ok {
		return
	}
	// Every snapshotting deployment is visited, not the oldest batch: a
	// platform-wide batch filled with releases still waiting for approval
	// would stop every other approved release from resuming
	// (ADR-20261020085706 amendment 13).
	var work []state.BuildImageWork
	err := l.pageOperatorDeployments(ctx, state.OperatorDeploymentFilter{
		ExcludeMCPTaskHolds: true,
		Statuses:            []state.DeploymentStatus{state.DeploySnapshotting},
		OldestFirst:         true,
		Limit:               staleDeploymentBatchSize,
	}, func(rows []state.Deployment) int {
		for _, d := range rows {
			if len(d.ReleaseCommand) == 0 || d.EnvironmentWorkloadHeld() {
				continue
			}
			if _, err := l.store.ReleaseAppTaskByDeployment(ctx, d.ID); !errors.Is(err, state.ErrNotFound) {
				continue
			}
			work = append(work, state.BuildImageWork{AppID: d.AppID, DeploymentID: d.ID, NodeID: l.handler.nodeName})
		}
		return len(rows)
	}, &l.releaseApprovalOffset)
	if err != nil {
		l.log.Warn("imaged: list releases held for approval", "err", err)
	}
	l.recoverImageWork(ctx, work, "resume")
}

// releaseApprovalPending reports whether d waits for its approvals before its
// release command, which the stale sweep must not cancel: the policy's
// timeout, up to api.ReleaseApprovalTimeoutMax, bounds the wait instead.
func (l *Loop) releaseApprovalPending(ctx context.Context, d state.Deployment) bool {
	if len(d.ReleaseCommand) == 0 || d.Status != state.DeploySnapshotting {
		return false
	}
	app, err := state.AppForDeployment(ctx, l.store, d)
	if err != nil {
		return false
	}
	app, err = state.AppForDeploymentRuntime(app, d)
	if err != nil {
		return false
	}
	wait, err := approval.LoadReleaseCommandWait(ctx, l.store, app, d, l.now())
	if err != nil {
		l.log.Warn("imaged: read release approval before stale cancellation", "deployment", d.ID, "err", err)
		return true // decide on the next sweep rather than cancel blind
	}
	// An approved release the break-glass pause holds waits too (amendment 13).
	return wait.Pending() || wait.Waits && wait.Status.State == api.ReleaseApprovalStateApproved && l.handler != nil && l.handler.releasePauseOn()
}
