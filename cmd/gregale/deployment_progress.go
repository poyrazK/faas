package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type deploymentProgressSnapshot struct {
	status       string
	rolloutState string
	step         int
	total        int
	traffic      int
	reason       string
	decision     string
}

// renderDeploymentProgress emits one human-readable line when a deployment's
// lifecycle or rollout position changes. Keeping the snapshot local to the
// wait command avoids a noisy line every polling interval while still making
// each canary transition visible to an operator.
func renderDeploymentProgress(w io.Writer, d api.DeploymentResponse, previous *deploymentProgressSnapshot) *deploymentProgressSnapshot {
	current := deploymentProgressSnapshot{
		status:       d.Status,
		rolloutState: d.RolloutState,
		step:         d.CanaryStep,
		total:        d.CanaryTotalSteps,
		traffic:      d.TrafficPercent,
		reason:       d.RolloutAbortedReason,
	}
	if d.RolloutDecision != nil {
		current.decision = d.RolloutDecision.Rule + "@" + d.RolloutDecision.DecidedAt
	}
	if previous != nil && *previous == current {
		return previous
	}
	if d.CanaryTotalSteps <= 0 {
		_, _ = fmt.Fprintf(w, "Deployment: %s\n", nonEmptyProgressState(d.Status, "unknown"))
		return &current
	}
	step := d.CanaryStep + 1
	if d.RolloutState == rolloutStateComplete || step > d.CanaryTotalSteps {
		step = d.CanaryTotalSteps
	}
	state := nonEmptyProgressState(d.RolloutState, d.Status)
	if d.RolloutState == rolloutStateAborted && d.RolloutAbortedReason != "" {
		line := fmt.Sprintf("Rollout: %d%% traffic · step %d/%d · %s · %s", d.TrafficPercent, step, d.CanaryTotalSteps, state, d.RolloutAbortedReason)
		if why := rolloutDecisionOfKind(d, api.RolloutDecisionAbort); why != "" {
			line += " · " + why
		}
		_, _ = fmt.Fprintln(w, line)
		return &current
	}
	line := fmt.Sprintf("Rollout: %d%% traffic · step %d/%d · %s", d.TrafficPercent, step, d.CanaryTotalSteps, state)
	if previous != nil && current.decision != previous.decision {
		if why := rolloutDecisionOfKind(d, api.RolloutDecisionAdvance); why != "" {
			line += " · " + why
		}
	}
	_, _ = fmt.Fprintln(w, line)
	return &current
}

func nonEmptyProgressState(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// rolloutHeldNoticeAfter is how long a canary step may sit unchanged before
// `deployment wait --rollout` says so.
const rolloutHeldNoticeAfter = 5 * time.Minute

// rolloutHeldNotice tracks one notice per canary step. On production-us,
// `deployment wait --rollout` printed nothing for ten minutes while meterd
// held a canary at its first step ("insufficient request samples"). That
// reason is logged only by meterd. The wait now says the step is held and
// what promotion needs.
type rolloutHeldNotice struct {
	step     int
	notified bool
	// held is the step and rule of the last recorded hold printed
	// (ADR-1447), so a changed reason is printed again.
	held string
}

func (n *rolloutHeldNotice) maybeWarn(w io.Writer, d api.DeploymentResponse, now time.Time) {
	if d.CanaryTotalSteps <= 0 || d.Status != statusLive || d.RolloutState == rolloutStateComplete ||
		d.RolloutState == rolloutStateAborted || d.CanaryStepStartedAt == nil {
		return
	}
	if n.step != d.CanaryStep {
		n.step, n.notified = d.CanaryStep, false
	}
	if d.RolloutHold != nil {
		key := fmt.Sprintf("%d/%s", d.CanaryStep, d.RolloutHold.Rule)
		if key != n.held {
			n.held, n.notified = key, true
			_, _ = fmt.Fprintf(w, "Rollout held at %d%% traffic (step %d/%d): %s\n",
				d.TrafficPercent, d.CanaryStep+1, d.CanaryTotalSteps, describeRolloutDecision(d.RolloutHold))
		}
		return
	}
	held := now.Sub(*d.CanaryStepStartedAt)
	if n.notified || held < rolloutHeldNoticeAfter {
		return
	}
	n.notified = true
	_, _ = fmt.Fprintf(w, "Rollout held at %d%% traffic (step %d/%d) for %s. Promotion waits for conclusive health:\n"+
		"  both this deployment and the stable one need request samples in the step window, with no 5xx or latency regression.\n"+
		"  Send traffic to the app, or inspect it with: gregale deployment summary %s --app <slug>\n",
		d.TrafficPercent, d.CanaryStep+1, d.CanaryTotalSteps, held.Truncate(time.Second), d.ID)
}

// rolloutDecisionOfKind describes the deployment's recorded decision only
// when it explains an outcome of that kind (ADR-1447).
func rolloutDecisionOfKind(d api.DeploymentResponse, kind string) string {
	if d.RolloutDecision == nil || d.RolloutDecision.Kind != kind {
		return ""
	}
	return describeRolloutDecision(d.RolloutDecision)
}
