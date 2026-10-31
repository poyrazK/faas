// Package schedule decides when a scheduled production release starts
// (ADR-20261021152201). It holds no state: apid loads a schedule, its release
// and the release's live siblings, and asks this package what to do.
package schedule

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// What to do with a due schedule.
const (
	ActionWait  = "wait"
	ActionStart = "start"
	ActionEnd   = "end"
)

// Outcome codes of schedules that end without starting.
const (
	CodeDeploymentFailed = "deployment_failed"
	CodeSuperseded       = "newer_release_serving"
	CodeReleasedByHand   = "released_manually"
	CodeNotReady         = "not_ready"
	CodeStartRefused     = "start_refused"
	CodeReplaced         = "replaced"
	CodeCancelled        = "cancelled_by_user"
	CodeAppDeleted       = "app_deleted"
)

// Decision is what to do with one due schedule. State, Code and Detail
// describe how an ended schedule ends.
type Decision struct {
	Action string
	State  string
	Code   string
	Detail string
}

func end(stateName, code, detail string) Decision {
	return Decision{Action: ActionEnd, State: stateName, Code: code, Detail: detail}
}

// Parked reports whether d waits live without traffic and without a canary
// ladder, as a scheduled release does until it starts.
func Parked(d state.Deployment) bool {
	return d.Status == state.DeployLive && d.TrafficPercent == 0 && d.CanaryTotalSteps == 0
}

// InFlight reports whether d is a canary that is still rolling out.
func InFlight(d state.Deployment) bool {
	rollout := state.NormalizeRolloutState(d.RolloutState)
	return d.Status == state.DeployLive && d.CanaryTotalSteps > 0 && (rollout == "pending" || rollout == "rolling_out")
}

// Serving reports whether any sibling takes traffic.
func Serving(siblings []state.Deployment) bool {
	for _, s := range siblings {
		if s.Status == state.DeployLive && s.TrafficPercent > 0 {
			return true
		}
	}
	return false
}

// Ref names a release as vN, or by ID before it has a revision.
func Ref(d state.Deployment) string {
	if d.Revision > 0 {
		return fmt.Sprintf("v%d", d.Revision)
	}
	return d.ID
}

// Decide picks what to do with a due schedule at now. siblings are the app's
// other live releases in the release's scope. Freezes and approvals are
// checked by the caller once Decide says to start.
func Decide(sr state.ScheduledRelease, d state.Deployment, siblings []state.Deployment, now time.Time) Decision {
	switch d.Status {
	case state.DeployFailed, state.DeployCancelled:
		return end(state.ScheduledReleaseCancelled, CodeDeploymentFailed, "the release's build did not finish: "+string(d.Status))
	case state.DeploySuperseded:
		return end(state.ScheduledReleaseSuperseded, CodeSuperseded, "a newer release replaced it before its time")
	case state.DeployLive:
	default:
		return WaitOrExpire(sr, now, "its build had not finished")
	}
	if !Parked(d) {
		return end(state.ScheduledReleaseCancelled, CodeReleasedByHand, "it was given traffic before its time")
	}
	for _, s := range siblings {
		if s.Revision > d.Revision && (s.TrafficPercent > 0 || InFlight(s)) {
			return end(state.ScheduledReleaseSuperseded, CodeSuperseded, Ref(s)+" was released after it and serves now")
		}
	}
	for _, s := range siblings {
		if InFlight(s) {
			return WaitOrExpire(sr, now, Ref(s)+" was still rolling out")
		}
	}
	return Decision{Action: ActionStart}
}

// Blocked ends a schedule that may not start, with the reason.
func Blocked(code, detail string) Decision {
	return end(state.ScheduledReleaseBlocked, code, detail)
}

// ClipDetail fits an outcome detail to scheduled_releases.outcome_detail.
func ClipDetail(detail string) string {
	const max = 500
	if len(detail) <= max {
		return detail
	}
	cut := max - 3
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut] + "..."
}

// WaitOrExpire waits for the next pass, or expires a schedule that has
// waited api.ScheduledReleaseStartGrace past its time; why says what it was
// waiting for.
func WaitOrExpire(sr state.ScheduledRelease, now time.Time, why string) Decision {
	if now.Sub(sr.ReleaseAt) >= api.ScheduledReleaseStartGrace {
		return end(state.ScheduledReleaseExpired, CodeNotReady,
			fmt.Sprintf("%s %d minutes after its time", why, int(api.ScheduledReleaseStartGrace.Minutes())))
	}
	return Decision{Action: ActionWait}
}

// UnoverriddenFreezes returns the active freezes a schedule's override does
// not cover.
func UnoverriddenFreezes(active []state.ReleaseFreeze, overridden []string) []state.ReleaseFreeze {
	covered := make(map[string]bool, len(overridden))
	for _, id := range overridden {
		covered[id] = true
	}
	var out []state.ReleaseFreeze
	for _, f := range active {
		if !covered[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// Ladder resolves the canary a schedule starts with into the stamp the store
// applies. A nil spec or the "none" preset is the zero ladder: all traffic
// at once.
func Ladder(spec *api.CanaryPresetSpec) (state.ScheduledReleaseLadder, *api.Problem) {
	if spec == nil || spec.Preset == "" || spec.Preset == "none" {
		return state.ScheduledReleaseLadder{}, nil
	}
	var (
		preset canary.Preset
		stages json.RawMessage
	)
	if spec.Preset == "custom" {
		custom, err := canary.LookupCustomPreset(spec.Stages)
		if err != nil {
			return state.ScheduledReleaseLadder{}, api.ErrInvalidCanaryPreset(err.Error())
		}
		raw, err := json.Marshal(spec.Stages)
		if err != nil {
			return state.ScheduledReleaseLadder{}, api.ErrInvalidCanaryPreset(err.Error())
		}
		preset, stages = custom, raw
	} else {
		p, ok := canary.LookupPreset(spec.Preset)
		if !ok {
			return state.ScheduledReleaseLadder{}, api.ErrInvalidCanaryPreset(spec.Preset)
		}
		preset = p
	}
	first, ok := preset.StageAt(0)
	if !ok || preset.TotalSteps() == 0 {
		return state.ScheduledReleaseLadder{}, nil
	}
	return state.ScheduledReleaseLadder{Preset: preset.Name, Stages: stages, TotalSteps: preset.TotalSteps(), FirstPercent: first.Percent}, nil
}

// Spec decodes a stored canary choice; nil means all traffic at once.
func Spec(raw json.RawMessage) (*api.CanaryPresetSpec, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var spec api.CanaryPresetSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("decode scheduled canary: %w", err)
	}
	return &spec, nil
}

// Encode stores a canary choice; nil stores nothing.
func Encode(spec *api.CanaryPresetSpec) json.RawMessage {
	if spec == nil {
		return nil
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil
	}
	return raw
}

// RolloutSummary describes how a schedule's rollout starts, for people.
func RolloutSummary(spec *api.CanaryPresetSpec) string {
	if spec == nil || spec.Preset == "" || spec.Preset == "none" {
		return "all traffic at once"
	}
	return spec.Preset + " canary"
}

// View renders a schedule for the API. channelNames are the names of its
// notification channels.
func View(sr state.ScheduledRelease, app string, channelNames []string) api.ScheduledRelease {
	if channelNames == nil {
		channelNames = []string{}
	}
	out := api.ScheduledRelease{
		ID: sr.ID, App: app, DeploymentID: sr.DeploymentID, Revision: sr.Revision,
		ReleaseAt: sr.ReleaseAt.UTC().Format(time.RFC3339), State: sr.State, NotifyChannels: channelNames,
		OverrideFreeze: sr.OverrideFreeze, OverriddenFreezes: sr.OverrideFreezeIDs, OutcomeCode: sr.OutcomeCode,
		OutcomeDetail: sr.OutcomeDetail, ScheduledBy: sr.ScheduledBy,
		CreatedAt: sr.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: sr.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if len(out.OverriddenFreezes) == 0 {
		out.OverriddenFreezes = nil
	}
	if spec, err := Spec(sr.Canary); err == nil {
		out.Canary = spec
	}
	if sr.StartedAt != nil {
		out.StartedAt = sr.StartedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// Notice snapshots what a schedule's channels are told when it starts or
// cannot start.
func Notice(event string, app state.App, d state.Deployment, sr state.ScheduledRelease, detail, reviewURL string) alertchannels.ReleaseNotice {
	commit := d.CommitSHA
	if len(commit) > 12 {
		commit = commit[:12]
	}
	notice := alertchannels.ReleaseNotice{
		Event: event, App: app.Slug, DeploymentID: d.ID, Revision: d.Revision, DeployedBy: d.DeployedBy,
		Commit: commit, Reason: d.Reason, ReleaseAt: sr.ReleaseAt.UTC().Format(time.RFC3339), Detail: detail,
		ReviewURL: reviewURL,
	}
	if spec, err := Spec(sr.Canary); err == nil {
		notice.Rollout = RolloutSummary(spec)
	}
	return notice
}
