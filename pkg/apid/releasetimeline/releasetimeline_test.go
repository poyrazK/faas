package releasetimeline_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/releasetimeline"
)

var t0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }

func ptr(t time.Time) *time.Time { return &t }

func decision(kind, rule, summary string, traffic int, decided time.Time, gates ...api.ReleaseGateResult) *api.RolloutDecision {
	return &api.RolloutDecision{Kind: kind, Rule: rule, Summary: summary, TrafficPercent: traffic,
		DecidedAt: decided.Format(time.RFC3339Nano), Gates: gates}
}

func auditRow(id int64, minutes int, kind, actor string, data any) releasetimeline.AuditRow {
	raw, _ := json.Marshal(data)
	return releasetimeline.AuditRow{ID: id, At: at(minutes), Kind: kind, Actor: actor, Data: raw}
}

func live(mutate func(*api.DeploymentResponse)) api.DeploymentResponse {
	d := api.DeploymentResponse{ID: "d2", Status: "live", Revision: 42, TrafficPercent: 100, CreatedAt: t0.Format(time.RFC3339)}
	if mutate != nil {
		mutate(&d)
	}
	return d
}

func TestPhase(t *testing.T) {
	stable := &releasetimeline.Release{ID: "d1", Revision: 41}
	cases := []struct {
		name    string
		in      releasetimeline.Inputs
		phase   string
		summary string
	}{
		{"building", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.Status = "building" })},
			api.ReleasePhaseBuilding, "Building"},
		{"failed", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.Status, d.ErrorCode = "failed", "build_timeout" })},
			api.ReleasePhaseFailed, "Build failed: build_timeout"},
		{"aborted canary", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) {
			d.RolloutState, d.RolloutAbortedAt, d.TrafficPercent = "aborted", ptr(at(9)), 0
			d.RolloutDecision = decision(api.RolloutDecisionAbort, api.RolloutRuleAbortReleaseGate, "orders fell 41%", 10, at(9))
		})}, api.ReleasePhaseRolledBack, "Rolled back: orders fell 41%"},
		{"post-release rollback", releasetimeline.Inputs{AutoRollbackReason: "post-release 5xx", Deployment: live(nil)},
			api.ReleasePhaseRolledBack, "Rolled back: post-release 5xx"},
		{"scheduled", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) {
			d.TrafficPercent = 0
			d.ScheduledRelease = &api.ScheduledRelease{State: api.ScheduledReleaseStatePending, ReleaseAt: "2026-10-23T09:00:00Z"}
		})}, api.ReleasePhaseScheduled, "Scheduled for 2026-10-23 09:00 UTC"},
		{"awaiting approval", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.CanaryTotalSteps, d.TrafficPercent = 4, 10 }),
			Approval: &api.ReleaseApprovalStatus{State: api.ReleaseApprovalStateWaiting, RequiredApprovals: 2, Approvals: 1}},
			api.ReleasePhaseAwaitingApproval, "Waiting for 1 more approval"},
		{"superseded", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.Status, d.TrafficPercent = "superseded", 0 }),
			ReplacedBy: &releasetimeline.Release{ID: "d3", Revision: 43, At: at(60)}}, api.ReleasePhaseSuperseded, "Replaced by v43"},
		{"held", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) {
			d.CanaryTotalSteps, d.CanaryStep, d.TrafficPercent, d.RolloutState = 4, 1, 10, "rolling_out"
			d.RolloutHold = decision(api.RolloutDecisionHold, api.RolloutRuleHoldInsufficient, "12 of 50 requests", 10, at(5))
		})}, api.ReleasePhaseHeld, "Held at step 2 of 4, 10% of traffic: 12 of 50 requests"},
		{"rolling out", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) {
			d.CanaryTotalSteps, d.CanaryStep, d.TrafficPercent, d.RolloutState = 4, 2, 50, "rolling_out"
		})}, api.ReleasePhaseRollingOut, "Rolling out: step 3 of 4, 50% of traffic"},
		{"watching", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.RolloutState, d.RolloutCompletedAt = "complete", ptr(at(10)) }),
			Watched: true, WatchWindow: 30 * time.Minute, Stable: stable, Now: at(20)}, api.ReleasePhaseWatching, "Released; watched until 09:40 UTC"},
		{"released after the watch", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.RolloutState, d.RolloutCompletedAt = "complete", ptr(at(10)) }),
			Watched: true, WatchWindow: 30 * time.Minute, Stable: stable, Now: at(41)}, api.ReleasePhaseReleased, "Released to 100% of traffic"},
		{"manual split", releasetimeline.Inputs{Deployment: live(func(d *api.DeploymentResponse) { d.TrafficPercent = 30 })},
			api.ReleasePhaseReleased, "Serving 30% of traffic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := releasetimeline.Build(tc.in)
			if got.Phase != tc.phase || got.Summary != tc.summary {
				t.Fatalf("phase = %q %q, want %q %q", got.Phase, got.Summary, tc.phase, tc.summary)
			}
		})
	}
}

func TestEventsTellTheReleaseInOrder(t *testing.T) {
	gate := api.ReleaseGateResult{Name: "orders", Kind: "custom_metric", Passed: true, Summary: "orders averaged 52"}
	people := map[string]string{"account:a1": "ana@example.com"}
	in := releasetimeline.Inputs{
		App: "shop", Watched: true, WatchWindow: 30 * time.Minute, Now: at(120),
		Stable: &releasetimeline.Release{ID: "d1", Revision: 41},
		Deployment: live(func(d *api.DeploymentResponse) {
			d.CanaryTotalSteps, d.CanaryStep, d.RolloutState, d.RolloutCompletedAt = 4, 4, "complete", ptr(at(40))
			d.DeployedBy, d.CommitSHA, d.PRNumber, d.Reason = "ana@example.com", "0123456789abcdef", 7, "fix checkout"
		}),
		Approval: &api.ReleaseApprovalStatus{State: api.ReleaseApprovalStateApproved, RequiredApprovals: 1, Approvals: 1, AfterStage: 1,
			WaitingSince: at(12).Format(time.RFC3339), Decisions: []api.ReleaseApprovalDecision{
				{Approver: "bo@example.com", Role: "admin", Decision: "approve", Counted: true, DecidedAt: at(20).Format(time.RFC3339)}}},
		Actor: func(raw string) string {
			if p, ok := people[raw]; ok {
				return p
			}
			return "Gregale"
		},
		Audit: []releasetimeline.AuditRow{
			// Given newest first, as the store lists them.
			auditRow(9, 40, "deploy.rollout_completed", "meterd:safedeploy", map[string]any{"final_step": 4}),
			auditRow(8, 30, "deploy.traffic_changed", "meterd:canary_progression", map[string]any{"from_percent": 10, "to_percent": 50, "to_step": 2,
				"decision": decision(api.RolloutDecisionAdvance, api.RolloutRuleAdvanceHealthy, "5xx 0.1% vs 0.2%", 10, at(30), gate)}),
			auditRow(7, 18, "deploy.rollout_held", "meterd:canary_progression", map[string]any{
				"decision": decision(api.RolloutDecisionHold, api.RolloutRuleHoldAwaitingApproval, "waiting for 1 approval", 10, at(18))}),
			auditRow(6, 13, "deploy.rollout_held", "meterd:canary_progression", map[string]any{
				"decision": decision(api.RolloutDecisionHold, api.RolloutRuleHoldAwaitingApproval, "waiting for 1 approval", 10, at(13))}),
			auditRow(5, 10, "deploy.traffic_changed", "account:a1", map[string]any{"from_percent": 1, "to_percent": 10, "to_step": 1,
				"decision": decision(api.RolloutDecisionAdvance, api.RolloutRuleAdvanceManual, "", 1, at(10))}),
			auditRow(4, 2, "deploy.rollout_started", "meterd:safedeploy", map[string]any{"canary_preset": "balanced", "canary_total": 4}),
			auditRow(3, 1, "deploy.health_probe_failed", "meterd:safedeploy", map[string]any{"secret": "never shown"}),
			auditRow(2, 0, "deploy.created", "account:a1", map[string]any{}),
		},
	}
	got := releasetimeline.Build(in)
	var lines []string
	for _, e := range got.Events {
		lines = append(lines, e.At[11:16]+" "+e.Kind+" "+e.Actor+" "+e.Summary)
	}
	want := []string{
		"09:00 created ana@example.com Deployed by ana@example.com commit 0123456789ab PR #7: fix checkout",
		"09:01 note Gregale A health probe failed",
		"09:02 rollout_started Gregale Rollout started: balanced canary, 4 steps",
		"09:10 advanced ana@example.com Advanced to 10% of traffic (step 2 of 4): advance manual",
		"09:12 approval_requested Gregale Waiting for 1 approval after step 1",
		"09:13 held Gregale Held at 10% of traffic: waiting for 1 approval",
		"09:20 approved bo@example.com Approved by bo@example.com (admin)",
		"09:30 advanced Gregale Advanced to 50% of traffic (step 3 of 4): 5xx 0.1% vs 0.2%",
		"09:40 completed Gregale Rollout completed: 100% of traffic",
		"10:10 watch_passed  Post-release watch passed: no regression in 30 minutes",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got.Events[5].Repeats != 1 {
		t.Fatalf("hold repeats = %d, want 1 (two holds with the same rule)", got.Events[5].Repeats)
	}
	if len(got.Gates) != 1 || got.Gates[0].Name != "orders" {
		t.Fatalf("gates = %+v, want the newest judged gates", got.Gates)
	}
	if got.Phase != api.ReleasePhaseReleased || got.StableRevision != 41 || got.WatchUntil != "2026-10-22T10:10:00Z" || got.Approval == nil {
		t.Fatalf("timeline = %+v", got)
	}
}

func TestRolledBackReleaseHasNoWatchPassed(t *testing.T) {
	in := releasetimeline.Inputs{Watched: true, WatchWindow: 30 * time.Minute, Now: at(120), AutoRollbackReason: "post-release 5xx",
		Stable:     &releasetimeline.Release{ID: "d1", Revision: 41},
		Deployment: live(func(d *api.DeploymentResponse) { d.Status, d.RolloutState, d.RolloutCompletedAt = "superseded", "complete", ptr(at(10)) }),
		Audit: []releasetimeline.AuditRow{auditRow(1, 25, "deploy.rolled_back", "apid:post_release_watch", map[string]any{"action": "abort",
			"decision": decision(api.RolloutDecisionAbort, api.RolloutRuleAbortPostRelease5xx, "5xx 4% vs 0.2%", 100, at(25))})},
	}
	got := releasetimeline.Build(in)
	if got.Phase != api.ReleasePhaseRolledBack {
		t.Fatalf("phase = %s", got.Phase)
	}
	for _, e := range got.Events {
		if e.Kind == api.ReleaseEventWatchPassed {
			t.Fatalf("a rolled-back release passed its watch: %+v", got.Events)
		}
	}
	if last := got.Events[len(got.Events)-1]; last.Kind != api.ReleaseEventRolledBack || last.Summary != "Rolled back: 5xx 4% vs 0.2%" || last.Decision == nil {
		t.Fatalf("last event = %+v", last)
	}
}

func TestScheduleAndUnknownAuditKinds(t *testing.T) {
	in := releasetimeline.Inputs{Now: at(200), Deployment: live(func(d *api.DeploymentResponse) {
		d.ScheduledRelease = &api.ScheduledRelease{State: api.ScheduledReleaseStateBlocked, ReleaseAt: at(60).Format(time.RFC3339),
			ScheduledBy: "ana@example.com", CreatedAt: at(5).Format(time.RFC3339), UpdatedAt: at(60).Format(time.RFC3339),
			OutcomeCode: "release_frozen", OutcomeDetail: "a deploy freeze is active"}
	}), Audit: []releasetimeline.AuditRow{auditRow(1, 70, "deploy.something_new", "apid", map[string]any{"x": 1})}}
	got := releasetimeline.Build(in)
	var kinds []string
	for _, e := range got.Events {
		kinds = append(kinds, e.Kind+": "+e.Summary)
	}
	want := "created: Deployed|scheduled: Scheduled for 2026-10-22 10:00 UTC|schedule_blocked: Scheduled release could not start: a deploy freeze is active|note: Recorded something_new"
	if strings.Join(kinds, "|") != want {
		t.Fatalf("events = %s", strings.Join(kinds, "|"))
	}
}
