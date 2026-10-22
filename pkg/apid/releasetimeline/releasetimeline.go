// Package releasetimeline composes one release's phase and history from
// what Gregale already records about it (ADR-20261022120744): the
// deployment row, its audit rows with the ADR-1447 decisions, its approval
// state and its schedule. It is pure, so apid serves it and tests pin it
// without a store.
package releasetimeline

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AuditRow is one deployment_audit row.
type AuditRow struct {
	ID    int64
	At    time.Time
	Kind  string
	Actor string
	Data  json.RawMessage
}

// Release names another release of the app.
type Release struct {
	ID       string
	Revision int
	// At is when a replacing release took over.
	At time.Time
}

// Inputs is everything the timeline is built from.
type Inputs struct {
	// Deployment is the release as GET /v1/deployments/{id} serves it, with
	// its rollout decision, current hold and schedule.
	Deployment api.DeploymentResponse
	App        string
	// AutoRollbackReason is set when an automatic rollback replaced the
	// release after its rollout.
	AutoRollbackReason string
	// Watched means the post-release watch judges the release after its
	// rollout completes (ADR-1445).
	Watched     bool
	WatchWindow time.Duration
	Stable      *Release
	ReplacedBy  *Release
	Audit       []AuditRow
	// AuditTruncated means older audit rows were not read.
	AuditTruncated bool
	Approval       *api.ReleaseApprovalStatus
	// Actor names an audit row's actor for people; nil keeps it as written.
	Actor func(string) string
	Now   time.Time
}

// Build returns the release's timeline.
func Build(in Inputs) api.ReleaseTimeline {
	d := in.Deployment
	out := api.ReleaseTimeline{DeploymentID: d.ID, App: in.App, Revision: d.Revision, Scope: d.Scope, Status: d.Status,
		TrafficPercent: d.TrafficPercent, CanaryStep: d.CanaryStep, CanaryTotalSteps: d.CanaryTotalSteps, CreatedAt: d.CreatedAt,
		DeployedBy: d.DeployedBy, CommitSHA: d.CommitSHA, PRNumber: d.PRNumber, Tag: d.Tag, Reason: d.Reason,
		Schedule: d.ScheduledRelease, Truncated: in.AuditTruncated, Events: []api.ReleaseTimelineEvent{}}
	if in.Stable != nil {
		out.StableDeploymentID, out.StableRevision = in.Stable.ID, in.Stable.Revision
	}
	if in.ReplacedBy != nil {
		out.ReplacedByDeploymentID, out.ReplacedByRevision = in.ReplacedBy.ID, in.ReplacedBy.Revision
	}
	if in.Approval != nil && in.Approval.State != api.ReleaseApprovalStateNotRequired {
		out.Approval = in.Approval
	}
	watchUntil, watched := watchEnd(in)
	if watched {
		out.WatchUntil = watchUntil.Format(time.RFC3339)
	}
	out.Phase, out.Summary = phase(in, out.Approval, watchUntil, watched)
	b := builder{in: in}
	b.created()
	b.schedule(d.ScheduledRelease)
	b.approval(out.Approval)
	b.audit()
	if watched && !in.Now.Before(watchUntil) && out.Phase != api.ReleasePhaseRolledBack {
		b.add(watchUntil, api.ReleaseEventWatchPassed, fmt.Sprintf("Post-release watch passed: no regression in %s", minutes(in.WatchWindow)), "", nil, nil)
	}
	if in.ReplacedBy != nil && !in.ReplacedBy.At.IsZero() {
		b.add(in.ReplacedBy.At, api.ReleaseEventSuperseded, "Replaced by "+revisionName(in.ReplacedBy.Revision, in.ReplacedBy.ID), "", nil, nil)
	}
	sort.SliceStable(b.events, func(i, j int) bool { return b.events[i].at.Before(b.events[j].at) })
	for _, e := range b.events {
		out.Events = append(out.Events, e.ReleaseTimelineEvent)
	}
	out.Gates = latestGates(b.events, d)
	return out
}

// watchEnd is when the post-release watch stops judging the release.
func watchEnd(in Inputs) (time.Time, bool) {
	d := in.Deployment
	if !in.Watched || d.RolloutCompletedAt == nil || in.WatchWindow <= 0 || in.Stable == nil {
		return time.Time{}, false
	}
	return d.RolloutCompletedAt.UTC().Add(in.WatchWindow), true
}

// phase is the first matching phase of ADR-20261022120744's table.
func phase(in Inputs, approval *api.ReleaseApprovalStatus, watchUntil time.Time, watched bool) (string, string) {
	d := in.Deployment
	switch d.Status {
	case "pending", "building", "imaging", "snapshotting":
		return api.ReleasePhaseBuilding, "Building"
	case "failed":
		if d.ErrorCode != "" {
			return api.ReleasePhaseFailed, "Build failed: " + d.ErrorCode
		}
		return api.ReleasePhaseFailed, "Build failed"
	case "cancelled":
		return api.ReleasePhaseCancelled, "Cancelled before release"
	}
	if d.RolloutAbortedAt != nil || d.RolloutState == "aborted" || in.AutoRollbackReason != "" {
		return api.ReleasePhaseRolledBack, "Rolled back: " + rolledBackWhy(in)
	}
	if s := d.ScheduledRelease; s != nil && s.State == api.ScheduledReleaseStatePending {
		return api.ReleasePhaseScheduled, "Scheduled for " + humanTime(s.ReleaseAt)
	}
	if approval != nil && approval.State == api.ReleaseApprovalStateWaiting {
		return api.ReleasePhaseAwaitingApproval, fmt.Sprintf("Waiting for %s", plural(approval.Remaining(), "more approval"))
	}
	if d.Status == "superseded" {
		if in.ReplacedBy != nil {
			return api.ReleasePhaseSuperseded, "Replaced by " + revisionName(in.ReplacedBy.Revision, in.ReplacedBy.ID)
		}
		return api.ReleasePhaseSuperseded, "Replaced by a newer release"
	}
	if inFlight(d) {
		where := fmt.Sprintf("step %s, %d%% of traffic", stepOf(d.CanaryStep, d.CanaryTotalSteps), d.TrafficPercent)
		if d.RolloutHold != nil {
			return api.ReleasePhaseHeld, fmt.Sprintf("Held at %s: %s", where, decisionText(d.RolloutHold))
		}
		return api.ReleasePhaseRollingOut, "Rolling out: " + where
	}
	if watched && in.Now.Before(watchUntil) {
		return api.ReleasePhaseWatching, "Released; watched until " + watchUntil.Format("15:04 UTC")
	}
	if d.TrafficPercent >= 100 {
		return api.ReleasePhaseReleased, "Released to 100% of traffic"
	}
	return api.ReleasePhaseReleased, fmt.Sprintf("Serving %d%% of traffic", d.TrafficPercent)
}

func inFlight(d api.DeploymentResponse) bool {
	state := strings.TrimSpace(d.RolloutState)
	return d.Status == "live" && d.CanaryTotalSteps > 0 && (state == "" || state == "pending" || state == "rolling_out")
}

// rolledBackWhy prefers the abort decision's summary, then the recorded
// reason.
func rolledBackWhy(in Inputs) string {
	d := in.Deployment
	if dec := d.RolloutDecision; dec != nil && dec.Kind == api.RolloutDecisionAbort {
		return decisionText(dec)
	}
	for _, reason := range []string{d.RolloutAbortedReason, in.AutoRollbackReason} {
		if strings.TrimSpace(reason) != "" {
			return reason
		}
	}
	return "the rollout was aborted"
}

// latestGates are the results of the newest decision that judged gates.
func latestGates(events []event, d api.DeploymentResponse) []api.ReleaseGateResult {
	var newest *api.RolloutDecision
	consider := func(dec *api.RolloutDecision) {
		if dec == nil || len(dec.Gates) == 0 {
			return
		}
		if newest == nil || decidedAt(dec).After(decidedAt(newest)) {
			newest = dec
		}
	}
	for _, e := range events {
		consider(e.Decision)
	}
	consider(d.RolloutDecision)
	consider(d.RolloutHold)
	if newest == nil {
		return nil
	}
	return newest.Gates
}

func decidedAt(d *api.RolloutDecision) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, d.DecidedAt)
	return at
}

// decisionText is a decision's summary, or its rule when it has none.
func decisionText(d *api.RolloutDecision) string {
	if s := strings.TrimSpace(d.Summary); s != "" {
		return s
	}
	return strings.ReplaceAll(d.Rule, "_", " ")
}

// stepOf renders a zero-based canary step as "2 of 4".
func stepOf(step, total int) string {
	n := step + 1
	if n > total {
		n = total
	}
	return fmt.Sprintf("%d of %d", n, total)
}

func revisionName(revision int, id string) string {
	if revision > 0 {
		return fmt.Sprintf("v%d", revision)
	}
	return id
}

func humanTime(rfc3339 string) string {
	at, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return at.UTC().Format("2006-01-02 15:04 UTC")
}

func minutes(d time.Duration) string {
	return plural(int(d/time.Minute), "minute")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
