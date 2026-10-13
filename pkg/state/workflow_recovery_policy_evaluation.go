package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

type WorkflowRecoveryPolicyEvaluationStore interface {
	EvaluateWorkflowRecoveryPolicy(context.Context, string, string, api.WorkflowRecoveryPolicyEvaluationRequest) (api.WorkflowRecoveryPolicyEvaluation, error)
}
type recoveryEvaluationReview struct {
	eligibility *api.WorkflowRecoveryFailureEligibility
	source      string
	evidenceID  string
	review      api.WorkflowRecoveryPolicyReview
	workflow    string
}
type recoveryEvaluationReplay struct {
	prepared []time.Time
	last     time.Time
	existing map[string]bool
}

func recoveryEvaluationWindow(account, app string, req api.WorkflowRecoveryPolicyEvaluationRequest, now time.Time) (recoveryHistoryCursor, error) {
	if err := req.Validate(); err != nil {
		return recoveryHistoryCursor{}, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	c, _, err := recoveryHistoryWindow(account, app, api.WorkflowRecoveryHistoryOptions{Workflow: req.Workflow, CreatedAfter: req.CreatedAfter, CreatedBefore: req.CreatedBefore}, now)
	return c, err
}
func recoveryEvaluationLookback(current, proposed api.WorkflowRecoveryPolicy) time.Duration {
	return max(time.Hour, time.Duration(current.CooldownSeconds)*time.Second, time.Duration(proposed.CooldownSeconds)*time.Second)
}
func recoveryEvaluationReviewLess(a, b recoveryEvaluationReview) bool {
	if !a.review.ObservedAt.Equal(b.review.ObservedAt) {
		return a.review.ObservedAt.Before(b.review.ObservedAt)
	}
	if a.review.ID == b.review.ID {
		return a.evidenceID < b.evidenceID
	}
	return canonicalMemUUID(a.review.ID) < canonicalMemUUID(b.review.ID)
}
func recoveryEvaluationEligibility(p api.WorkflowRecoveryPolicy, r recoveryEvaluationReview, d api.WorkflowRecoveryPolicyDecision) api.WorkflowRecoveryPolicyAssessment {
	a := api.WorkflowRecoveryPolicyAssessment{Eligibility: "excluded", Preparation: "excluded", ExecutionPressure: "not_evaluated"}
	exclude := func(reason string) api.WorkflowRecoveryPolicyAssessment { a.Reason = reason; return a }
	unknown := func(reason string) api.WorkflowRecoveryPolicyAssessment {
		a.Eligibility = "unknown"
		a.Preparation = "unknown"
		a.Reason = reason
		return a
	}
	if !p.Enabled {
		return exclude("policy_disabled")
	}
	if r.workflow == "" {
		return unknown("workflow_evidence_missing")
	}
	if !recoveryPolicyHas(p.WorkflowNames, r.workflow) {
		return exclude("workflow_filter")
	}
	if !recoveryPolicyHas(p.Triggers, d.Trigger) {
		return exclude("trigger_filter")
	}
	if d.Trigger != "saved_failure" || d.Action != "retry" {
		return unknown("unsupported_historical_trigger")
	}
	if d.RunID == "" || d.ResumeCount < 0 {
		return unknown("target_evidence_missing")
	}
	if d.ResumeCount >= p.MaxResumeCount {
		return exclude("generation_limit")
	}
	if len(p.FailureStatuses) > 0 && d.FailureStatus != "" {
		if !recoveryPolicyHas(p.FailureStatuses, d.FailureStatus) {
			return exclude("failure_status_filter")
		}
	} else if len(p.FailureStatuses) > 0 {
		if len(r.review.Policy.FailureStatuses) == 0 {
			return unknown("failure_status_not_recorded")
		}
		all, any := true, false
		for _, status := range r.review.Policy.FailureStatuses {
			match := recoveryPolicyHas(p.FailureStatuses, status)
			all = all && match
			any = any || match
		}
		if !any {
			return exclude("failure_status_filter")
		}
		if !all {
			return unknown("failure_status_ambiguous")
		}
	}
	if d.PreflightOutcome != "" && d.PreflightOutcome != "eligible" {
		return exclude("historical_preflight_" + d.PreflightOutcome)
	}
	if r.source == "failure_inventory" {
		return unknown("historical_preflight_not_captured")
	}
	switch d.Outcome {
	case "prepared", "already_prepared", "generation_limit", "cooldown", "hourly_budget":
	default:
		return unknown("historical_preflight_unproven")
	}
	a.Eligibility = "qualifies"
	a.Preparation = "would_prepare"
	a.Reason = "retained_eligibility_evidence"
	return a
}
func (b *recoveryEvaluationReplay) begin(at time.Time) {
	first := 0
	for first < len(b.prepared) && !b.prepared[first].After(at.Add(-time.Hour)) {
		first++
	}
	b.prepared = b.prepared[first:]
}
func (b *recoveryEvaluationReplay) assess(p api.WorkflowRecoveryPolicy, r recoveryEvaluationReview, d api.WorkflowRecoveryPolicyDecision, batch *int, hourly int, last *time.Time) api.WorkflowRecoveryPolicyAssessment {
	a := recoveryEvaluationEligibility(p, r, d)
	if a.Eligibility != "qualifies" {
		return a
	}
	key := recoveryPolicyTargetKey(canonicalMemUUID(d.RunID), d.ResumeCount, d.Action)
	switch {
	case b.existing[key]:
		a.Preparation = "already_prepared"
	case !b.last.IsZero() && b.last.Add(time.Duration(p.CooldownSeconds)*time.Second).After(r.review.ObservedAt):
		a.Preparation = "cooldown"
	case len(b.prepared)+*batch >= p.HourlyActionBudget:
		a.Preparation = "hourly_budget"
	default:
		*batch++
		b.existing[key] = true
	}
	if p.Mode != "automatic_retry" {
		a.ExecutionPressure = "not_enabled"
	} else if hourly >= p.HourlyExecutionBudget {
		a.ExecutionPressure = "observed_hourly_budget"
	} else if last != nil && last.Add(time.Duration(p.CooldownSeconds)*time.Second).After(r.review.ObservedAt) {
		a.ExecutionPressure = "observed_cooldown"
	} else {
		a.ExecutionPressure = "insufficient_history"
	}
	return a
}
func (b *recoveryEvaluationReplay) finish(at time.Time, count int) {
	if count > 0 {
		b.last = at
	}
	for range count {
		b.prepared = append(b.prepared, at)
	}
}
func recoveryEvaluationCount(t *api.WorkflowRecoveryPolicyEvaluationTotals, a api.WorkflowRecoveryPolicyAssessment) {
	t.Candidates++
	switch a.Preparation {
	case "would_prepare":
		t.WouldPrepare++
	case "excluded":
		t.Excluded++
	case "unknown":
		t.Unknown++
	case "cooldown":
		t.Cooldown++
	case "hourly_budget":
		t.PreparationBudget++
	case "already_prepared":
		t.Deduplicated++
	}
	switch a.ExecutionPressure {
	case "observed_hourly_budget":
		t.ExecutionBudgetPressure++
	case "observed_cooldown":
		t.ExecutionCooldownPressure++
	}
}
func recoveryEvaluationPolicyCopy(p api.WorkflowRecoveryPolicy) api.WorkflowRecoveryPolicy {
	p = recoveryPolicyClone(p)
	p.WorkflowNames = append([]string{}, p.WorkflowNames...)
	p.Triggers = append([]string{}, p.Triggers...)
	p.FailureStatuses = append([]string{}, p.FailureStatuses...)
	return p
}
func evaluateRecoveryPolicy(ctx context.Context, c recoveryHistoryCursor, current api.WorkflowRecoveryPolicyResponse, req api.WorkflowRecoveryPolicyEvaluationRequest, reviews []recoveryEvaluationReview, executions []api.WorkflowRecoveryHistoryEntry, failureSnapshots ...recoveryFailureSnapshot) (api.WorkflowRecoveryPolicyEvaluation, error) {
	current.Policy = recoveryEvaluationPolicyCopy(current.Policy)
	out := api.WorkflowRecoveryPolicyEvaluation{Current: current, Proposed: recoveryEvaluationPolicyCopy(req.Policy), ObservedAt: c.ObservedAt, CreatedAfter: c.Start, CreatedBefore: c.End, Candidates: []api.WorkflowRecoveryPolicyEvaluationCandidate{}, CoverageNotes: []string{
		"Terminal workflow failures are captured prospectively regardless of policy filters. Durable reviews add historical preflight facts. Older reviews require retained source rows; historical operational pause state is not reconstructed.",
		"Exact failure status and preflight outcomes are used when captured. Legacy policy filters only prove a possible status set; narrower overlapping filters remain unknown. No live run state is substituted for past evidence.",
		"Preparation replay is conditional: both policies start with empty preparation budgets, cooldown and deduplication state at the window start. Unknown candidates are not charged. Reviews run chronologically by time and ID, with decisions ordered by position.",
		"Execution checks report lower-bound pressure from recorded applications across the app, including a warmup of the longer policy cooldown or one hour before the window. Missing pressure does not prove budget or admission availability; prospective capture and 30-day / 10000-event retention can omit evidence.",
		"No executions, success predictions, counterfactual results, failure-pause replay, quota/admission guarantees or notification captures are performed.",
	}}
	sort.Slice(reviews, func(i, j int) bool { return recoveryEvaluationReviewLess(reviews[i], reviews[j]) })
	if len(reviews) > api.WorkflowRecoveryPolicyEvaluationReviewsMax {
		out.ReviewsTruncated = true
		reviews = reviews[:api.WorkflowRecoveryPolicyEvaluationReviewsMax]
	}
	var snapshot recoveryFailureSnapshot
	if len(failureSnapshots) > 0 {
		snapshot = failureSnapshots[0]
	}
	reviews, out.FailureCoverage = recoveryEvaluationWithFailures(reviews, snapshot)
	sort.Slice(reviews, func(i, j int) bool { return recoveryEvaluationReviewLess(reviews[i], reviews[j]) })
	out.CoverageNotes = append(out.CoverageNotes, "Failure coverage is partial: capture starts prospectively and recording-age retention, per-app capacity, source review sampling and candidate limits can omit evidence. Never-reviewed failures retain unknown retry eligibility; live state is never substituted for missing checks.")
	sort.Slice(executions, func(i, j int) bool {
		if !executions[i].OccurredAt.Equal(executions[j].OccurredAt) {
			return executions[i].OccurredAt.Before(executions[j].OccurredAt)
		}
		return executions[i].ID < executions[j].ID
	})
	if len(executions) > api.WorkflowRecoveryPolicyEvaluationExecutionsMax {
		out.ExecutionsTruncated = true
		executions = executions[:api.WorkflowRecoveryPolicyEvaluationExecutionsMax]
	}
	seen := map[string]bool{}
	unique := executions[:0]
	for _, e := range executions {
		key := canonicalMemUUID(e.ActionID)
		if key != "" && !seen[key] {
			seen[key] = true
			unique = append(unique, e)
		}
	}
	executions = unique
	out.ObservedExecutions = len(executions)
	left, right := 0, 0
	currentReplay := recoveryEvaluationReplay{existing: map[string]bool{}}
	proposedReplay := recoveryEvaluationReplay{existing: map[string]bool{}}
	for _, r := range reviews {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if r.source != "failure_inventory" {
			out.InspectedReviews++
		}
		at := r.review.ObservedAt
		for right < len(executions) && executions[right].OccurredAt.Before(at) {
			right++
		}
		for left < right && !executions[left].OccurredAt.After(at.Add(-time.Hour)) {
			left++
		}
		var last *time.Time
		if right > 0 {
			value := executions[right-1].OccurredAt
			last = &value
		}
		proof := []string{}
		for i := right - 1; i >= left && len(proof) < api.WorkflowRecoveryReportEvidenceMax; i-- {
			proof = append(proof, executions[i].ID)
		}
		if right > 0 && len(proof) == 0 {
			proof = append(proof, executions[right-1].ID)
		}
		currentReplay.begin(at)
		proposedReplay.begin(at)
		currentBatch, proposedBatch := 0, 0
		sort.Slice(r.review.Decisions, func(i, j int) bool {
			a, b := r.review.Decisions[i], r.review.Decisions[j]
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			return a.RunID < b.RunID
		})
		for _, d := range r.review.Decisions {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if len(out.Candidates) >= api.WorkflowRecoveryPolicyEvaluationCandidatesMax {
				out.CandidatesTruncated = true
				break
			}
			candidate := api.WorkflowRecoveryPolicyEvaluationCandidate{ReviewID: r.review.ID, JobID: r.review.JobID, RunID: d.RunID, Workflow: r.workflow, ObservedAt: at, PolicyVersion: r.review.PolicyVersion, Position: d.Position, ResumeCount: d.ResumeCount, Trigger: d.Trigger, RecordedOutcome: d.Outcome, HistoricalFailureStatuses: append([]string{}, r.review.Policy.FailureStatuses...), ObservedHourlyExecutions: right - left, LastObservedExecutionAt: last, ExecutionEvidenceIDs: append([]string{}, proof...)}
			candidate.EvidenceSource = "policy_review"
			if r.source == "failure_inventory" {
				candidate.EvidenceSource = r.source
				candidate.FailureEvidenceID = r.evidenceID
				candidate.LaterEligibility = failureEligibilityAssessment(r, d, current.Policy, req.Policy, c.ObservedAt)
			} else {
				candidate.CandidateEvidenceID = r.evidenceID
			}
			if d.FailureStatus != "" {
				candidate.HistoricalFailureStatuses = []string{d.FailureStatus}
			}
			candidate.Current = currentReplay.assess(current.Policy, r, d, &currentBatch, right-left, last)
			candidate.Proposed = proposedReplay.assess(req.Policy, r, d, &proposedBatch, right-left, last)
			recoveryEvaluationCount(&out.CurrentTotals, candidate.Current)
			recoveryEvaluationCount(&out.ProposedTotals, candidate.Proposed)
			out.Candidates = append(out.Candidates, candidate)
		}
		currentReplay.finish(at, currentBatch)
		proposedReplay.finish(at, proposedBatch)
		if out.CandidatesTruncated {
			break
		}
	}
	out.CoverageNotes = append(out.CoverageNotes, "Later eligibility observations check retained state after a failure; they never change historical preparation totals. Checks older than 15 minutes are stale. Even fresh qualifying checks do not prove current budget, quota or admission availability.")
	if out.ReviewsTruncated || out.CandidatesTruncated || out.ExecutionsTruncated || out.FailureCoverage.Truncated {
		out.CoverageNotes = append(out.CoverageNotes, "Sample truncated: oldest matching reviews, candidates and application evidence are retained up to the stated limits; comparisons are partial.")
	}
	if len(out.Candidates) == 0 {
		out.CoverageNotes = append(out.CoverageNotes, "No retained matching review candidates were found. This does not establish that no historical failures occurred.")
	}
	return out, nil
}
