package state

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Compare only explicit decision snapshots. Metadata omission does not clear a decision.
func compareWorkflowDecisionConditions(previous *api.OperationWorkflowStateHistoryEntry, current api.OperationWorkflowStateHistoryEntry) *api.OperationWorkflowConditionComparison {
	out := &api.OperationWorkflowConditionComparison{Status: "compared", Changes: []api.OperationWorkflowConditionChange{}}
	compare := func(source, group string, before, after *api.OperationBusinessDecision, prior, next api.OperationWorkflowConditionEvidence) {
		if after == nil {
			return
		}
		left, right := map[string]api.OperationBusinessConditionResult{}, map[string]api.OperationBusinessConditionResult{}
		if before != nil {
			for _, c := range before.Conditions {
				left[c.ID] = c
			}
		}
		for _, c := range after.Conditions {
			right[c.ID] = c
		}
		ids := map[string]bool{}
		for id := range left {
			ids[id] = true
		}
		for id := range right {
			ids[id] = true
		}
		ordered := make([]string, 0, len(ids))
		for id := range ids {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		for _, id := range ordered {
			b, hasBefore := left[id]
			a, hasAfter := right[id]
			change := api.OperationWorkflowConditionChange{Source: source, GroupID: group, ConditionID: id, CurrentEvidence: next}
			if before != nil {
				ref := prior
				change.PreviousEvidence = &ref
			}
			if hasBefore {
				value := b
				change.Before = &value
			}
			if hasAfter {
				value := a
				change.After = &value
			}
			switch {
			case !hasBefore:
				change.Status = "newly_reported"
				change.Explanation = "Application newly reported this condition in the current decision; its earlier result is unreported."
			case !hasAfter:
				change.Status = "unreported"
				change.Explanation = "Application did not report this condition in the current decision; this does not establish a changed or resolved business condition."
			case b != a:
				change.Status = "changed"
				change.Explanation = "Application reported a different condition result or public description; this does not establish a cause for the decision."
			default:
				continue
			}
			if len(out.Changes) == api.OperationWorkflowConditionChangesMax {
				out.Truncated = true
				continue
			}
			out.Changes = append(out.Changes, change)
		}
	}
	if current.ChangeReason != nil {
		var before *api.OperationBusinessDecision
		var prior api.OperationWorkflowConditionEvidence
		if previous.ChangeReason != nil {
			before = &previous.ChangeReason.Decision
			prior = workflowConditionEvidence(*previous, previous.ChangeReason.MilestoneID, *before, "")
		}
		after := &current.ChangeReason.Decision
		compare("reason", "", before, after, prior, workflowConditionEvidence(current, current.ChangeReason.MilestoneID, *after, ""))
	}
	beforePaths := map[string]api.OperationWorkflowPathSelection{}
	for _, p := range previous.SelectedPaths {
		beforePaths[p.GroupID] = p
	}
	paths := api.CloneOperationWorkflowPathSelections(current.SelectedPaths)
	sort.Slice(paths, func(i, j int) bool { return paths[i].GroupID < paths[j].GroupID })
	for _, path := range paths {
		if path.DecisionMilestoneID == "" {
			continue
		}
		after := workflowPathConditionDecision(current, path)
		var before *api.OperationBusinessDecision
		var prior api.OperationWorkflowConditionEvidence
		if p, ok := beforePaths[path.GroupID]; ok && p.DecisionMilestoneID != "" {
			d := workflowPathConditionDecision(*previous, p)
			before = &d
			prior = workflowConditionEvidence(*previous, p.DecisionMilestoneID, d, p.PathID)
		}
		compare("path", path.GroupID, before, &after, prior, workflowConditionEvidence(current, path.DecisionMilestoneID, after, path.PathID))
	}
	return out
}
func workflowPathConditionDecision(row api.OperationWorkflowStateHistoryEntry, p api.OperationWorkflowPathSelection) api.OperationBusinessDecision {
	return api.OperationBusinessDecision{Workflow: row.Workflow, InstanceID: row.InstanceID, Code: p.DecisionCode, Description: p.DecisionDescription, RuleID: p.DecisionRuleID, RuleVersion: p.DecisionRuleVersion, Conditions: p.DecisionConditions}
}
func workflowConditionEvidence(row api.OperationWorkflowStateHistoryEntry, id string, d api.OperationBusinessDecision, path string) api.OperationWorkflowConditionEvidence {
	return api.OperationWorkflowConditionEvidence{ReportID: row.ID, OperationID: row.OperationID, Revision: row.Revision, MilestoneID: id, DecisionCode: d.Code, RuleID: d.RuleID, RuleVersion: d.RuleVersion, PathID: path}
}

// Derive the qualifying report's comparison from the exact bounded cohort history.
func workflowContributorConditionComparison(instance workflowPerformanceInstance, evidence api.OperationWorkflowDecisionOutcomeEvidence) *api.OperationWorkflowConditionComparison {
	observations := workflowDecisionOutcomeReports(instance)
	var current *api.OperationWorkflowStateHistoryEntry
	for _, o := range observations {
		if o.History.ID == evidence.ReportID && o.History.OperationID == evidence.OperationID {
			value := o.History
			current = &value
			break
		}
	}
	if current == nil {
		return &api.OperationWorkflowConditionComparison{Status: "baseline_unavailable", Changes: []api.OperationWorkflowConditionChange{}}
	}
	sameRun := func(row api.OperationWorkflowStateHistoryEntry) bool {
		return row.Workflow == current.Workflow && row.InstanceID == current.InstanceID && row.PlatformTenantID == current.PlatformTenantID
	}
	counts := map[int64]int{}
	for _, o := range observations {
		if sameRun(o.History) {
			counts[o.History.Revision]++
		}
	}
	var previous *api.OperationWorkflowStateHistoryEntry
	for _, o := range observations {
		row := o.History
		if !sameRun(row) {
			continue
		}
		if row.ID == current.ID && row.OperationID == current.OperationID {
			if counts[row.Revision] > 1 || previous != nil && counts[previous.Revision] > 1 {
				return &api.OperationWorkflowConditionComparison{Status: "ambiguous_revision", Changes: []api.OperationWorkflowConditionChange{}}
			}
			return compareWorkflowRevisions(previous, row).ConditionComparison
		}
		value := row
		previous = &value
	}
	return &api.OperationWorkflowConditionComparison{Status: "baseline_unavailable", Changes: []api.OperationWorkflowConditionChange{}}
}
