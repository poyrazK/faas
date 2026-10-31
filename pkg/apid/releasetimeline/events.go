package releasetimeline

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type event struct {
	api.ReleaseTimelineEvent
	at   time.Time
	rule string
}

type builder struct {
	in     Inputs
	events []event
}

func (b *builder) add(at time.Time, kind, summary, actor string, traffic *int, decision *api.RolloutDecision) {
	if at.IsZero() {
		return
	}
	e := event{at: at.UTC(), ReleaseTimelineEvent: api.ReleaseTimelineEvent{At: at.UTC().Format(time.RFC3339), Kind: kind,
		Summary: summary, Actor: actor, TrafficPercent: traffic, Decision: decision}}
	if decision != nil {
		e.rule = decision.Rule
	}
	b.events = append(b.events, e)
}

func (b *builder) actor(raw string) string {
	if b.in.Actor == nil {
		return raw
	}
	return b.in.Actor(raw)
}

// created describes who released what.
func (b *builder) created() {
	d := b.in.Deployment
	parts := []string{"Deployed"}
	if d.DeployedBy != "" {
		parts = append(parts, "by "+d.DeployedBy)
	}
	if d.CommitSHA != "" {
		parts = append(parts, "commit "+shortSHA(d.CommitSHA))
	}
	if d.PRNumber > 0 {
		parts = append(parts, fmt.Sprintf("PR #%d", d.PRNumber))
	}
	if d.Tag != "" {
		parts = append(parts, "tag "+d.Tag)
	}
	summary := strings.Join(parts, " ")
	if d.Reason != "" {
		summary += ": " + d.Reason
	}
	b.add(parseTime(d.CreatedAt), api.ReleaseEventCreated, summary, d.DeployedBy, nil, nil)
}

// schedule adds the release's schedule and how it ended.
func (b *builder) schedule(s *api.ScheduledRelease) {
	if s == nil {
		return
	}
	b.add(parseTime(s.CreatedAt), api.ReleaseEventScheduled, "Scheduled for "+humanTime(s.ReleaseAt), s.ScheduledBy, nil, nil)
	switch s.State {
	case api.ScheduledReleaseStateStarted:
		b.add(parseTime(s.StartedAt), api.ReleaseEventScheduleStarted, "Scheduled release started", "Gregale", nil, nil)
	case api.ScheduledReleaseStateBlocked, api.ScheduledReleaseStateExpired:
		b.add(parseTime(s.UpdatedAt), api.ReleaseEventScheduleBlocked, "Scheduled release could not start: "+firstText(s.OutcomeDetail, s.OutcomeCode, s.State), "Gregale", nil, nil)
	case api.ScheduledReleaseStateCancelled:
		b.add(parseTime(s.UpdatedAt), api.ReleaseEventScheduleCancelled, "Schedule cancelled", "", nil, nil)
	case api.ScheduledReleaseStateSuperseded:
		b.add(parseTime(s.UpdatedAt), api.ReleaseEventScheduleCancelled, "Schedule dropped: a newer release replaced this one", "Gregale", nil, nil)
	}
}

// approval adds the wait for approval and each decision.
func (b *builder) approval(s *api.ReleaseApprovalStatus) {
	if s == nil {
		return
	}
	if s.WaitingSince != "" {
		summary := "Waiting for " + plural(s.RequiredApprovals, "approval")
		if s.AfterStage > 0 {
			summary += fmt.Sprintf(" after step %d", s.AfterStage)
		}
		b.add(parseTime(s.WaitingSince), api.ReleaseEventApprovalRequested, summary, "Gregale", nil, nil)
	}
	for _, d := range s.Decisions {
		kind, verb := api.ReleaseEventApproved, "Approved"
		if d.Decision == api.ReleaseApprovalDecisionReject {
			kind, verb = api.ReleaseEventRejected, "Rejected"
		}
		summary := fmt.Sprintf("%s by %s (%s)", verb, d.Approver, d.Role)
		if !d.Counted && d.Decision == api.ReleaseApprovalDecisionApprove {
			summary += ", not counted: the deployer"
		}
		if d.Comment != "" {
			summary += ": " + d.Comment
		}
		b.add(parseTime(d.DecidedAt), kind, summary, d.Approver, nil, nil)
	}
	if s.State == api.ReleaseApprovalStateExpired {
		b.add(parseTime(s.ExpiresAt), api.ReleaseEventApprovalExpired, "Nobody approved in time; the rollout was aborted", "Gregale", nil, nil)
	}
}

// auditData is the subset of audit payloads the timeline reads.
type auditData struct {
	Action        string               `json:"action"`
	Reason        string               `json:"reason"`
	FromPercent   *int                 `json:"from_percent"`
	ToPercent     *int                 `json:"to_percent"`
	ToStep        *int                 `json:"to_step"`
	CanaryPreset  string               `json:"canary_preset"`
	CanaryTotal   int                  `json:"canary_total"`
	Decision      *api.RolloutDecision `json:"decision"`
	PredecessorID string               `json:"predecessor_deployment_id"`
}

// notes are the fixed sentences for audit kinds the timeline does not
// explain further; the payload is never shown.
var notes = map[string]string{
	"deploy.health_probe_failed":      "A health probe failed",
	"deploy.health_recovered":         "Health probes recovered",
	"deploy.alert_rule_fired":         "An alert rule acted on this release",
	"deploy.scan_regressed":           "A security re-scan found blocking findings",
	"deploy.removed":                  "Release removed",
	"deploy.worker_drain_started":     "Worker drain started",
	"deploy.worker_drain_completed":   "Worker drain completed",
	"deploy.mcp_task_held":            "Promotion held for MCP tasks",
	"deploy.mcp_task_retry_requested": "MCP task hold retry requested",
	"deploy.mcp_task_retry_started":   "MCP task hold retry started",
	"deploy.mcp_task_retry_completed": "MCP task hold retry completed",
	"deploy.mcp_task_recovered":       "MCP task hold recovered",
	"deploy.mcp_task_hold_cancelled":  "MCP task hold cancelled",
	"deploy.mcp_admission":            "MCP admission recorded",
	"deploy.canary_step_advanced":     "Canary advanced",
}

// skipped audit kinds are covered by the created event.
var skipped = map[string]bool{"deploy.created": true, "deploy.requested": true, "deploy.source_ref": true, "deploy.local_tarball": true}

// audit adds the release's audit rows, oldest first, collapsing consecutive
// holds with the same rule.
func (b *builder) audit() {
	rows := append([]AuditRow(nil), b.in.Audit...)
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.Before(rows[j].At)
		}
		return rows[i].ID < rows[j].ID
	})
	for _, row := range rows {
		if skipped[row.Kind] {
			continue
		}
		var data auditData
		_ = json.Unmarshal(row.Data, &data)
		if data.Decision != nil && data.Decision.Rule == "" {
			data.Decision = nil
		}
		if row.Kind == "deploy.rollout_held" {
			b.hold(row, data)
			continue
		}
		kind, summary, traffic := b.describe(row.Kind, data)
		b.add(row.At, kind, summary, b.actor(row.Actor), traffic, data.Decision)
	}
}

// hold adds a hold, or counts it on the previous event when that is a hold
// with the same rule.
func (b *builder) hold(row AuditRow, data auditData) {
	if data.Decision == nil {
		return
	}
	if n := len(b.events); n > 0 {
		last := &b.events[n-1]
		if last.Kind == api.ReleaseEventHeld && last.rule == data.Decision.Rule {
			last.Repeats++
			last.Summary, last.Decision = holdSummary(data.Decision), data.Decision
			return
		}
	}
	traffic := data.Decision.TrafficPercent
	b.add(row.At, api.ReleaseEventHeld, holdSummary(data.Decision), b.actor(row.Actor), &traffic, data.Decision)
}

func holdSummary(d *api.RolloutDecision) string {
	return fmt.Sprintf("Held at %d%% of traffic: %s", d.TrafficPercent, decisionText(d))
}

// describe maps one audit row to an event kind and sentence.
func (b *builder) describe(kind string, data auditData) (string, string, *int) {
	total := b.in.Deployment.CanaryTotalSteps
	switch kind {
	case "deploy.rollout_started":
		if data.CanaryTotal > 0 {
			return api.ReleaseEventRolloutStarted, fmt.Sprintf("Rollout started: %s canary, %s", firstText(data.CanaryPreset, "custom"), plural(data.CanaryTotal, "step")), nil
		}
		return api.ReleaseEventRolloutStarted, "Rollout started", nil
	case "deploy.rollout_completed":
		full := 100
		return api.ReleaseEventCompleted, "Rollout completed: 100% of traffic", &full
	case "deploy.rollout_aborted":
		return api.ReleaseEventAborted, "Rollout aborted: " + why(data, "the rollout was stopped"), nil
	case "deploy.rolled_back":
		return api.ReleaseEventRolledBack, "Rolled back: " + why(data, "traffic returned to the previous release"), nil
	case "deploy.rollback_requested":
		return api.ReleaseEventRollbackRequested, "Rollback requested" + suffix(data.Reason), nil
	case "deploy.traffic_changed":
		return trafficEvent(data, total)
	}
	if note, ok := notes[kind]; ok {
		return api.ReleaseEventNote, note, nil
	}
	return api.ReleaseEventNote, "Recorded " + strings.TrimPrefix(kind, "deploy."), nil
}

// trafficEvent describes a canary advance, a promotion or a hand-set split.
func trafficEvent(data auditData, total int) (string, string, *int) {
	switch {
	case data.Action == "promote":
		full := 100
		return api.ReleaseEventAdvanced, "Promoted to 100% of traffic" + suffix(data.Reason), &full
	case data.Action == "advance" || (data.Decision != nil && data.Decision.Kind == api.RolloutDecisionAdvance):
		summary := "Advanced"
		if data.ToPercent != nil {
			summary += fmt.Sprintf(" to %d%% of traffic", *data.ToPercent)
		}
		if data.ToStep != nil && total > 0 {
			summary += " (step " + stepOf(*data.ToStep, total) + ")"
		}
		if data.Decision != nil {
			summary += ": " + decisionText(data.Decision)
		}
		return api.ReleaseEventAdvanced, summary, data.ToPercent
	case data.ToPercent != nil:
		from := ""
		if data.FromPercent != nil {
			from = fmt.Sprintf(" (was %d%%)", *data.FromPercent)
		}
		return api.ReleaseEventTrafficChanged, fmt.Sprintf("Traffic set to %d%%%s", *data.ToPercent, from), data.ToPercent
	}
	return api.ReleaseEventTrafficChanged, "Traffic changed", nil
}

func why(data auditData, fallback string) string {
	if data.Decision != nil {
		return decisionText(data.Decision)
	}
	return firstText(data.Reason, fallback)
}

func suffix(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return ": " + reason
}

func firstText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func parseTime(rfc3339 string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		return time.Time{}
	}
	return at
}
