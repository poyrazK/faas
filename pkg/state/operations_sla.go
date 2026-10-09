package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowStateSLABudget(spec api.OperationDefinitionSpec, state api.OperationWorkflowState) int64 {
	if state.Terminal {
		return 0
	}
	for _, step := range spec.WorkflowSteps {
		if step.Workflow == state.Workflow && effectiveWorkflowContractVersion(step.Version) == effectiveWorkflowContractVersion(state.ContractVersion) {
			return step.StateSLABudgetSeconds[state.State]
		}
	}
	return 0
}
func workflowStateSLAWarning(spec api.OperationDefinitionSpec, state api.OperationWorkflowState) int64 {
	if state.Terminal {
		return 0
	}
	for _, step := range spec.WorkflowSteps {
		if step.Workflow == state.Workflow && effectiveWorkflowContractVersion(step.Version) == effectiveWorkflowContractVersion(state.ContractVersion) {
			return step.StateSLAWarningPercent[state.State]
		}
	}
	return 0
}
func workflowSLAConfigured(spec api.OperationDefinitionSpec, state api.OperationWorkflowState) bool {
	for _, step := range spec.WorkflowSteps {
		if step.Workflow == state.Workflow && effectiveWorkflowContractVersion(step.Version) == effectiveWorkflowContractVersion(state.ContractVersion) && len(step.StateSLABudgetSeconds) > 0 {
			return true
		}
	}
	return false
}
func workflowStateSLA(current api.OperationWorkflowState, observations []workflowBottleneckObservation, budget, warning int64, at time.Time) *api.OperationWorkflowStateSLA {
	if budget < 1 || current.Terminal {
		return nil
	}
	out := &api.OperationWorkflowStateSLA{EvaluatedAt: at, BudgetSeconds: budget, Status: "unknown"}
	if warning > 0 {
		out.WarningPercent = &warning
	}
	rows := append([]workflowBottleneckObservation(nil), observations...)
	sort.Slice(rows, func(i, j int) bool { return workflowBottleneckHistoryLess(rows[j], rows[i]) })
	if len(rows) > api.OperationWorkflowBottleneckHistoryMax+1 {
		rows = rows[:api.OperationWorkflowBottleneckHistoryMax+1]
	}
	counts := map[int64]int{}
	for _, o := range rows {
		counts[o.History.Revision]++
	}
	if len(rows) > api.OperationWorkflowBottleneckHistoryMax {
		rows = rows[:api.OperationWorkflowBottleneckHistoryMax]
	}
	if len(rows) == 0 {
		return out
	}
	latest := rows[0].History
	if latest.ID != current.ReportID || latest.OperationID != current.OperationID || latest.Revision != current.Revision || latest.State != current.State || effectiveWorkflowContractVersion(latest.ContractVersion) != effectiveWorkflowContractVersion(current.ContractVersion) {
		return out
	}
	entered := latest.OccurredAt
	known := false
	for index, o := range rows {
		h := o.History
		if counts[h.Revision] != 1 || h.OccurredAt.IsZero() || h.OccurredAt.After(at) || effectiveWorkflowContractVersion(h.ContractVersion) != effectiveWorkflowContractVersion(current.ContractVersion) {
			return out
		}
		if index > 0 {
			newer := rows[index-1].History
			if h.Revision+1 != newer.Revision || h.OccurredAt.After(newer.OccurredAt) || newer.FromState != "" && newer.FromState != h.State {
				return out
			}
			if h.State != current.State {
				known = true
				break
			}
		}
		if o.SLABudgetSeconds != budget || o.SLAWarningPercent != warning {
			return out
		}
		entered = h.OccurredAt
		if h.Revision == 1 {
			known = true
			break
		}
	}
	if !known {
		return out
	}
	due := entered.Add(time.Duration(budget) * time.Second).UTC()
	elapsed := (at.UnixMicro() - entered.UnixMicro()) / 1_000_000
	remaining := budget - elapsed
	if remaining < 0 {
		remaining = 0
	}
	breached := elapsed - budget
	if breached < 0 {
		breached = 0
	}
	out.HistoryComplete = true
	out.EnteredAt = &entered
	out.DueAt = &due
	out.ElapsedSeconds = &elapsed
	out.RemainingSeconds = &remaining
	out.BreachedSeconds = &breached
	out.Status = "within_budget"
	if warning > 0 {
		// Multiply seconds by percent before converting to duration to avoid overflow.
		warnAt := entered.Add(time.Duration(budget*warning) * (time.Second / 100)).UTC()
		out.WarningAt = &warnAt
		if !at.Before(warnAt) {
			out.Status = "at_risk"
		}
	}
	if !at.Before(due) {
		out.Status = "breached"
	}
	return out
}
func workflowSLABreached(state api.OperationWorkflowState) bool {
	return state.SLA != nil && state.SLA.Status == "breached"
}
func workflowSLAAtRisk(state api.OperationWorkflowState) bool {
	return state.SLA != nil && state.SLA.Status == "at_risk"
}
func workflowSLARequiresAttention(state api.OperationWorkflowState) bool {
	return workflowSLABreached(state) || workflowSLAAtRisk(state)
}
func noteWorkflowSLAAttention(instance *api.OperationWorkflowInstanceSnapshot) {
	if instance != nil && instance.State != nil && workflowSLARequiresAttention(*instance.State) && instance.Decision != nil {
		instance.Decision.NeedsAttention = true
		if workflowSLAAtRisk(*instance.State) {
			instance.Decision.Explanation += " The observed current state visit reached its declared SLA warning threshold."
		} else {
			instance.Decision.Explanation += " The observed current state visit exceeded its declared SLA budget."
		}
	}
}

type workflowSLAVisits struct{ Evaluated, Breached int64 }

func workflowPerformanceSLAVisits(instance workflowPerformanceInstance, at time.Time) (map[workflowStateDurationKey]workflowSLAVisits, bool) {
	rows := append([]workflowBottleneckObservation(nil), instance.Observations...)
	sort.Slice(rows, func(i, j int) bool { return workflowBottleneckHistoryLess(rows[i], rows[j]) })
	result := map[workflowStateDurationKey]workflowSLAVisits{}
	for start := 0; start < len(rows); {
		first := rows[start]
		end := start + 1
		for end < len(rows) && rows[end].History.State == first.History.State && effectiveWorkflowContractVersion(rows[end].History.ContractVersion) == effectiveWorkflowContractVersion(first.History.ContractVersion) {
			end++
		}
		budget := first.SLABudgetSeconds
		for index := start + 1; index < end; index++ {
			if rows[index].SLABudgetSeconds != budget {
				return result, false
			}
		}
		if budget > 0 {
			through := at
			if end < len(rows) {
				through = rows[end].History.OccurredAt
			}
			key := workflowStateDurationKey{Version: effectiveWorkflowContractVersion(first.History.ContractVersion), State: first.History.State}
			count := result[key]
			count.Evaluated++
			if !through.Before(first.History.OccurredAt.Add(time.Duration(budget) * time.Second)) {
				count.Breached++
			}
			result[key] = count
		}
		start = end
	}
	return result, true
}
