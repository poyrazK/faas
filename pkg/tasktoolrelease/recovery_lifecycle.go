package tasktoolrelease

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RecoveryLifecycle separates retained recovery progress from live health checks.
// Approval state only describes an unconsumed immutable qualification.
type RecoveryLifecycle struct {
	ExecutorFenced bool   `json:"executor_fenced"`
	HandoverID     string `json:"handover_id,omitempty"`
	HandoverState  string `json:"handover_state"`
	Restore        string `json:"restore"`
	Quarantine     string `json:"quarantine"`
	Retirement     string `json:"retirement"`
}

// RecoveryNextStep is guidance, never a grant. Local observation reconciles an
// admitted attempt through the trusted adapter; inspection cannot query Task SQL.
type RecoveryNextStep struct {
	Action                   string   `json:"action"`
	RequiresOriginalBindings bool     `json:"requires_original_bindings"`
	Blockers                 []string `json:"blockers"`
}

func recoveryApprovalState(q *api.TaskToolRecoveryQualification, decision string, now time.Time) string {
	if q == nil {
		return "not_qualified"
	}
	if decision == "revoked" {
		return "revoked"
	}
	if !now.Before(q.ExpiresAt) {
		return "approval_expired"
	}
	if decision == "approved" {
		return "approved"
	}
	return "qualified"
}
func recoveryLifecycle(r api.TaskToolReleaseIntentRecord, now time.Time) *RecoveryLifecycle {
	e := r.Execution
	if e == nil || e.Recovery == nil && e.ExecutorExitedAt == nil {
		return nil
	}
	out := &RecoveryLifecycle{ExecutorFenced: true, HandoverState: "not_requested", Restore: "not_qualified", Quarantine: "not_qualified", Retirement: "not_qualified"}
	h := e.Recovery
	if h == nil {
		return out
	}
	out.HandoverID, out.HandoverState = h.ID, h.State
	out.Restore = recoveryApprovalState(h.Qualification, h.Decision, now)
	if h.Result != nil {
		out.Restore = "restored"
	}
	if q := h.Quarantine; q != nil {
		out.Quarantine = q.State
		if q.State == "qualified" {
			out.Quarantine = recoveryApprovalState(&q.Qualification, q.Decision, now)
		}
	}
	if q := h.Retirement; q != nil {
		out.Retirement = q.State
		if q.State == "qualified" {
			out.Retirement = recoveryApprovalState(&q.Qualification, q.Decision, now)
		}
	}
	return out
}
func recoveryClassification(l *RecoveryLifecycle) string {
	if l.HandoverState == "not_requested" {
		return "executor_exited"
	}
	if l.HandoverState == "stopping" {
		return "handover_pending"
	}
	if l.Retirement == "retired" || l.Retirement == "retirement_pending" {
		return l.Retirement
	}
	if l.Quarantine == "quarantined" {
		return "quarantined"
	}
	if l.Quarantine == "applying" {
		return "quarantine_applying"
	}
	if l.Restore == "restored" {
		return "restored"
	}
	return "executor_fenced"
}
func recoveryLifecycleBlockers(r api.TaskToolReleaseIntentRecord, action string, now time.Time) []string {
	blockers := []string{}
	e := r.Execution
	if e == nil {
		return blockers
	}
	if action == "resume" && (e.Recovery != nil || e.ExecutorExitedAt != nil) {
		blockers = append(blockers, "executor_permanently_fenced")
	}
	h := e.Recovery
	if action == "quarantine" && (h == nil || h.Result == nil) {
		blockers = append(blockers, "restore_result_not_recorded")
	}
	if h == nil {
		return blockers
	} // Preserve legacy owner-operated recovery guidance.
	if h.State != "settled" {
		blockers = append(blockers, "handover_not_settled")
	}
	l := recoveryLifecycle(r, now)
	state := ""
	switch action {
	case "restore":
		state = l.Restore
		if h.Result != nil {
			blockers = append(blockers, "restore_already_recorded")
		}
	case "quarantine":
		state = l.Quarantine
		if state == "applying" {
			blockers = append(blockers, "quarantine_attempt_already_admitted")
		}
		if state == "quarantined" {
			blockers = append(blockers, "quarantine_already_recorded")
		}
	case "retire":
		state = l.Retirement
		if h.Quarantine == nil || h.Quarantine.Result == nil {
			blockers = append(blockers, "quarantine_result_not_recorded")
		}
		if state == "retirement_pending" {
			blockers = append(blockers, "retirement_park_already_committed")
		}
		if state == "retired" {
			blockers = append(blockers, "retirement_already_recorded")
		}
	}
	if state == "approval_expired" || state == "revoked" {
		blockers = append(blockers, action+"_"+state)
	}
	return blockers
}
func recoveryNextStep(report RecoveryReport, r api.TaskToolReleaseIntentRecord, actions []RecoveryAction) *RecoveryNextStep {
	step := &RecoveryNextStep{Action: "owner_reconciliation", Blockers: []string{}}
	if report.Receipt == nil {
		step.Blockers = []string{"checkpoint_missing"}
		return step
	}
	if !recoveryPassed(report, "journal_stable") {
		step.Action = "inspect_again"
		step.Blockers = []string{"journal_not_stable"}
		return step
	}
	l := report.Lifecycle
	if !recoveryPassed(report, "recovery_timeline") {
		step.Action = "inspect_again"
		step.Blockers = []string{"recovery_timeline_unverified"}
		return step
	}
	switch {
	case l.HandoverState == "not_requested":
		step.Action = "request_handover"
		return step
	case l.HandoverState == "stopping":
		step.Action = "observe_handover"
		return step
	case l.Retirement == "retired":
		step.Action = "prepare_new_release"
		return step
	case l.Retirement == "retirement_pending":
		// A known identity/generation change cannot complete the original park.
		for _, app := range r.Intent.Apps {
			if app.Role == "worker" {
				for _, fact := range report.Checks {
					if (fact.Name == roleFact(app, "identity") || fact.Name == roleFact(app, "generation")) && fact.State == "failed" {
						step.Blockers = append(step.Blockers, fact.Name)
					}
				}
			}
		}
		if len(step.Blockers) == 0 {
			step.Action = "observe_retirement"
			step.RequiresOriginalBindings = true
		}
		return step
	case l.Quarantine == "applying":
		step.Action = "observe_quarantine"
		step.RequiresOriginalBindings = true
		return step
	}
	phase, state, action := "restore", l.Restore, "restore"
	if l.Restore == "restored" {
		phase, state, action = "quarantine", l.Quarantine, "quarantine"
	}
	if l.Quarantine == "quarantined" {
		phase, state, action = "retirement", l.Retirement, "retire"
	}
	for _, candidate := range actions {
		if candidate.Action == action && len(candidate.Blockers) > 0 {
			step.Blockers = slices.Clone(candidate.Blockers)
			return step
		}
	}
	switch state {
	case "not_qualified":
		step.Action = "qualify_" + phase
		step.RequiresOriginalBindings = true
	case "qualified":
		step.Action = "approve_" + phase
	case "approved":
		step.Action = "apply_" + phase
		step.RequiresOriginalBindings = true
	default:
		step.Blockers = []string{action + "_" + state}
	}
	return step
}

// Deadlines may be in the future; recorded observations and acknowledgements may
// not. A clock-skewed snapshot remains historical but gives no next apply step.
func recoveryTimelineVerified(r api.TaskToolReleaseIntentRecord, now time.Time) bool {
	e := r.Execution
	stamps := []*time.Time{e.ExecutorExitedAt}
	if h := e.Recovery; h != nil {
		stamps = append(stamps, &h.RequestedAt, h.SettledAt, h.DecidedAt)
		if h.Qualification != nil {
			stamps = append(stamps, &h.Qualification.ObservedAt)
		}
		if h.Result != nil {
			stamps = append(stamps, &h.Result.CheckedAt, &h.Result.CommittedAt)
		}
		if q := h.Quarantine; q != nil {
			stamps = append(stamps, &q.Qualification.ObservedAt, q.DecidedAt, q.ClaimedAt)
			if q.Result != nil {
				stamps = append(stamps, &q.Result.ObservedAt, &q.Result.RecordedAt)
			}
		}
		if q := h.Retirement; q != nil {
			stamps = append(stamps, &q.Qualification.ObservedAt, q.DecidedAt, q.ParkRequestedAt)
			if q.Result != nil {
				stamps = append(stamps, &q.Result.ObservedAt, &q.Result.RecordedAt)
			}
		}
	}
	for _, stamp := range stamps {
		if stamp != nil && stamp.After(now) {
			return false
		}
	}
	return true
}
