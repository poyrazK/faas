// Package approval decides whether a production release waits for people
// (ADR-20261020085706). It holds no state: apid loads an app's policy and a
// release's decisions and asks this package what they mean.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Gated reports whether a release needs approval before it takes more
// traffic. Releases whose rollout started before the policy, releases that
// completed a rollout and the app's serving release never wait, so a
// rollback never does. A scheduled release starts its rollout when its time
// comes (ADR-20261021152201), so a policy set while it waited gates it.
func Gated(policy *state.ReleaseApprovalPolicy, app state.App, d state.Deployment) bool {
	if policy == nil || !RequestMode(app) {
		return false
	}
	if d.Scope != "" && d.Scope != api.DefaultEnvScope {
		return false
	}
	if startedBefore(d, policy.CreatedAt) || d.RolloutCompletedAt != nil {
		return false
	}
	return d.Status != state.DeployLive || d.TrafficPercent != 100
}

// startedBefore reports whether d was created, and its rollout started,
// before t. A release not yet rolling out counts from its creation.
func startedBefore(d state.Deployment, t time.Time) bool {
	return d.CreatedAt.Before(t) && (d.RolloutStartedAt == nil || d.RolloutStartedAt.Before(t))
}

// ParkedNeedsCanary reports whether giving a parked release traffic by hand
// would skip the approvals its app requires: a production release waiting
// without traffic can only start through a schedule, whose canary can wait
// (ADR-20261021152201).
func ParkedNeedsCanary(policy *state.ReleaseApprovalPolicy, app state.App, d state.Deployment) bool {
	if policy == nil || !RequestMode(app) || (d.Scope != "" && d.Scope != api.DefaultEnvScope) {
		return false
	}
	return d.Status == state.DeployLive && d.TrafficPercent == 0 && d.TrafficPercentExplicit && d.CanaryTotalSteps == 0
}

// RequestMode reports whether the app serves request traffic through a
// canary; services, workers and jobs have no stage to wait in.
func RequestMode(app state.App) bool {
	mode := app.Manifest.ExecutionMode
	return mode == "" || mode == api.ExecutionModeRequest
}

// WaitStep is the zero-based canary step a release waits in: the policy's
// stage, or the last stage before 100% when the ladder is shorter. A release
// without a canary waits before any traffic increase.
func WaitStep(policy state.ReleaseApprovalPolicy, d state.Deployment) int {
	step := policy.AfterStage - 1
	if last := d.CanaryTotalSteps - 2; d.CanaryTotalSteps >= 2 && step > last {
		step = last
	}
	if step < 0 || d.CanaryTotalSteps < 2 {
		return 0
	}
	return step
}

// Status computes a release's approval state at now.
func Status(policy *state.ReleaseApprovalPolicy, app state.App, d state.Deployment, decisions []state.ReleaseApproval, now time.Time) api.ReleaseApprovalStatus {
	out := api.ReleaseApprovalStatus{DeploymentID: d.ID, App: app.Slug, State: api.ReleaseApprovalStateNotRequired,
		DeployedBy: d.DeployedBy, Decisions: []api.ReleaseApprovalDecision{}}
	if !Gated(policy, app, d) {
		return out
	}
	out.RequiredApprovals = policy.RequiredApprovals
	out.AfterStage = policy.AfterStage
	rejected := false
	for _, a := range decisions {
		counted := a.Decision == state.ReleaseApprovalApprove && (policy.AllowSelfApproval || !SelfApproval(d, a.ApproverAccountID))
		if counted {
			out.Approvals++
		}
		rejected = rejected || a.Decision == state.ReleaseApprovalReject
		out.Decisions = append(out.Decisions, api.ReleaseApprovalDecision{
			Approver: a.Approver, Role: a.ApproverRole, Decision: a.Decision, Comment: a.Comment,
			Counted: counted, DecidedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	since, waiting := waitingSince(*policy, d)
	if waiting {
		out.WaitingSince = since.UTC().Format(time.RFC3339)
		out.ExpiresAt = since.Add(time.Duration(policy.TimeoutMinutes) * time.Minute).UTC().Format(time.RFC3339)
	}
	switch {
	case rejected:
		out.State = api.ReleaseApprovalStateRejected
	case out.Approvals >= policy.RequiredApprovals:
		out.State = api.ReleaseApprovalStateApproved
	case waiting && !now.Before(since.Add(time.Duration(policy.TimeoutMinutes)*time.Minute)):
		out.State = api.ReleaseApprovalStateExpired
	default:
		out.State = api.ReleaseApprovalStateWaiting
	}
	return out
}

// SelfApproval reports whether approverAccountID deployed the release. A
// release with no recorded deployer (a GitHub push) has no self.
func SelfApproval(d state.Deployment, approverAccountID string) bool {
	return d.DeployedByUserID != "" && d.DeployedByUserID == approverAccountID
}

// waitingSince is when an in-flight canary entered its waiting stage.
func waitingSince(policy state.ReleaseApprovalPolicy, d state.Deployment) (time.Time, bool) {
	rollout := state.NormalizeRolloutState(d.RolloutState)
	if d.CanaryTotalSteps < 2 || d.CanaryStepStartedAt == nil || (rollout != "pending" && rollout != "rolling_out") {
		return time.Time{}, false
	}
	if d.CanaryStep < WaitStep(policy, d) {
		return time.Time{}, false
	}
	return *d.CanaryStepStartedAt, true
}

// Blocks reports whether status stops the release from taking more traffic.
func Blocks(status api.ReleaseApprovalStatus) bool {
	return status.State != api.ReleaseApprovalStateNotRequired && status.State != api.ReleaseApprovalStateApproved
}

// AdvanceBlocked reports whether advancing a canary from expectedStep needs
// approval the release does not have yet.
func AdvanceBlocked(policy *state.ReleaseApprovalPolicy, d state.Deployment, expectedStep int, status api.ReleaseApprovalStatus) bool {
	return policy != nil && Blocks(status) && expectedStep >= WaitStep(*policy, d)
}

// Problem is the refusal for a blocked release.
func Problem(status api.ReleaseApprovalStatus) *api.Problem {
	if status.State == api.ReleaseApprovalStateRejected {
		return api.ErrReleaseApprovalRejected(status)
	}
	return api.ErrReleaseApprovalRequired(status)
}

// HoldDecision is the hold_awaiting_approval rollout decision.
func HoldDecision(status api.ReleaseApprovalStatus, d state.Deployment, now time.Time) json.RawMessage {
	summary := fmt.Sprintf("waiting for %d more approval(s)", status.Remaining())
	return encode(api.ManualRolloutDecision(api.RolloutRuleHoldAwaitingApproval, summary, d.CanaryStep, d.TrafficPercent, now))
}

// AbortDecision is the rollout decision for a rejected or timed-out release.
func AbortDecision(rule, summary string, d state.Deployment, now time.Time) json.RawMessage {
	return encode(api.ManualRolloutDecision(rule, summary, d.CanaryStep, d.TrafficPercent, now))
}

func encode(d api.RolloutDecision) json.RawMessage {
	encoded, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return encoded
}

// PolicyView renders a policy for the API with its channels' names.
func PolicyView(p state.ReleaseApprovalPolicy, app string, channelNames []string) api.ReleaseApprovalPolicy {
	if channelNames == nil {
		channelNames = []string{}
	}
	return api.ReleaseApprovalPolicy{
		App: app, RequiredApprovals: p.RequiredApprovals, AfterStage: p.AfterStage, TimeoutMinutes: p.TimeoutMinutes,
		Approvers: p.Approvers, AllowSelfApproval: p.AllowSelfApproval, NotifyChannels: channelNames, UpdatedBy: p.UpdatedBy,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// noticeEvents maps an outbox event to the channel event it renders as.
var noticeEvents = map[string]string{
	state.ReleaseApprovalEventRequested: alertchannels.EventApprovalRequested,
	state.ReleaseApprovalEventApproved:  alertchannels.EventApprovalApproved,
	state.ReleaseApprovalEventRejected:  alertchannels.EventApprovalRejected,
	state.ReleaseApprovalEventExpired:   alertchannels.EventApprovalExpired,
}

// Notice snapshots what the channels are told about one approval event.
// actor and comment name the decision behind approved and rejected events.
func Notice(event string, app state.App, d state.Deployment, status api.ReleaseApprovalStatus, actor, comment, reviewURL string) alertchannels.ReleaseNotice {
	commit := d.CommitSHA
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return alertchannels.ReleaseNotice{
		Event: noticeEvents[event], App: app.Slug, DeploymentID: d.ID, Revision: d.Revision, DeployedBy: d.DeployedBy,
		Commit: commit, Reason: d.Reason, Stage: d.CanaryStep + 1, TotalStages: d.CanaryTotalSteps,
		TrafficPercent: d.TrafficPercent, Approvals: status.Approvals, Required: status.RequiredApprovals,
		ExpiresAt: status.ExpiresAt, Actor: actor, Comment: comment, ReviewURL: reviewURL,
	}
}

// RoleAllowed reports whether an organization role may decide under the
// policy's approvers setting.
func RoleAllowed(approvers string, role state.OrgRole) bool {
	switch role {
	case state.OrgRoleOwner, state.OrgRoleAdmin:
		return true
	case state.OrgRoleDeveloper:
		return approvers == api.ReleaseApproversDeveloper
	default:
		return false
	}
}

// releaseReader is the read surface the release-time check needs.
type releaseReader interface {
	ReleaseApprovalPolicy(ctx context.Context, appID string) (state.ReleaseApprovalPolicy, error)
	LiveDeploymentForScope(ctx context.Context, appID, scope string) (state.Deployment, error)
}

// CanaryRequired reports whether a production release of app must roll out
// through a canary because the app requires approvals. The app's first
// release, named environments, other scopes and non-request apps never need
// one. Stores without approvals need none.
func CanaryRequired(ctx context.Context, store any, app state.App, scope, environment string) (bool, error) {
	if environment != "" || (scope != "" && scope != api.DefaultEnvScope) || !RequestMode(app) {
		return false, nil
	}
	reader, ok := store.(releaseReader)
	if !ok {
		return false, nil
	}
	if _, err := reader.ReleaseApprovalPolicy(ctx, app.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("read release approval policy: %w", err)
	}
	if _, err := reader.LiveDeploymentForScope(ctx, app.ID, api.DefaultEnvScope); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("read live deployment: %w", err)
	}
	return true, nil
}

// TakesTrafficWithoutCanary reports whether a deploy request would give the
// release traffic without a canary to wait in. A dark deployment
// (traffic_percent 0) takes none.
func TakesTrafficWithoutCanary(req *api.CreateDeploymentRequest) bool {
	if req.Canary != nil && req.Canary.Preset != "none" {
		return false
	}
	return req.TrafficPercent == nil || *req.TrafficPercent > 0
}
