package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowRefreshProgressBaseline(account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, at time.Time) (*time.Time, error) {
	if opts.BaselineToken == "" {
		return nil, nil
	}
	token, err := parseWorkflowPerformanceToken(opts.BaselineToken, workflowPerformanceQuery(account, tenant, opts, operator), at)
	if err != nil {
		return nil, err
	}
	// A saved token anchors time and query, not reconstructible historical cohort membership.
	return &token.EvaluatedAt, nil
}
func workflowRefreshProgressHistoryReadable(instance workflowPerformanceInstance, at time.Time) bool {
	for _, observation := range instance.Observations {
		if observation.History.PublishedAt.IsZero() {
			return false
		}
	}
	b := calculateWorkflowBottlenecks(instance.Observations, &instance.Current, nil, at, false)
	for _, reason := range b.IncompleteReasons {
		if reason != "contract_changed" {
			return false
		}
	}
	return true
}
func workflowRefreshProgressBaselineInstance(instance workflowPerformanceInstance, at time.Time) (workflowPerformanceInstance, bool) {
	baseline := instance
	baseline.Observations = nil
	for _, observation := range workflowDecisionOutcomeReports(instance) {
		if !observation.History.PublishedAt.After(at) {
			baseline.Observations = append(baseline.Observations, observation)
		}
	}
	if len(baseline.Observations) == 0 {
		return baseline, false
	}
	last := baseline.Observations[len(baseline.Observations)-1]
	h := last.History
	baseline.Current = api.OperationWorkflowState{ReportID: h.ID, OperationID: h.OperationID, Workflow: h.Workflow, InstanceID: h.InstanceID, ContractVersion: h.ContractVersion, Revision: h.Revision, State: h.State, Terminal: last.Terminal, OccurredAt: h.OccurredAt, Blockers: h.Blockers}
	return baseline, true
}
func workflowRefreshProgressCounts(counts map[string]int64) []api.OperationWorkflowConditionRefreshProgressCount {
	out := []api.OperationWorkflowConditionRefreshProgressCount{}
	for _, status := range []string{"refreshed", "still_overdue", "unreported", "contract_changed", "budget_removed"} {
		out = append(out, api.OperationWorkflowConditionRefreshProgressCount{Status: status, ConditionCount: counts[status]})
	}
	return out
}
func workflowRefreshProgressChange(before api.OperationWorkflowDecisionOutcomeEvidence, after *api.OperationWorkflowDecisionOutcomeEvidence, contractChanged bool) api.OperationWorkflowConditionRefreshProgressChange {
	out := api.OperationWorkflowConditionRefreshProgressChange{Before: before, After: after}
	switch {
	case contractChanged:
		out.Status = "contract_changed"
		out.After = nil
		out.Explanation = "The workflow contract changed; the baseline result is not compared across contracts."
	case after == nil:
		out.Status = "unreported"
		out.Explanation = "The exact baseline condition and decision/rule identity is absent from the latest explicit contexts. This does not prove business resolution."
	default:
		out.NewerReport = after.Revision > before.Revision
		switch after.ConditionFreshness.Status {
		case "no_budget":
			out.Status = "budget_removed"
			out.Explanation = "The selected current fact has no configured freshness budget; this is not evidence of refreshed verification."
		case "overdue":
			out.Status = "still_overdue"
			out.Explanation = "The selected current report is overdue, including when a newer report was published but is already overdue."
		default:
			out.Status = "refreshed"
			out.Explanation = "A newer selected report is within its pinned reporting-age budget. This does not assert successful business verification."
		}
	}
	return out
}
func workflowRefreshProgress(instances []workflowPerformanceInstance, baselineAt, at time.Time) *api.OperationWorkflowConditionRefreshProgress {
	out := &api.OperationWorkflowConditionRefreshProgress{BaselineAt: baselineAt, EvaluatedAt: at, SampledWorkflowCount: int64(len(instances)), Items: []api.OperationWorkflowConditionRefreshProgressInstance{}}
	totals := map[string]int64{}
	ordered := append([]workflowPerformanceInstance(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return operationAttentionKey(a.Tenant, a.Subject, a.Current.Workflow, a.Current.InstanceID) < operationAttentionKey(b.Tenant, b.Subject, b.Current.Workflow, b.Current.InstanceID)
	})
	for _, instance := range ordered {
		if !workflowRefreshProgressHistoryReadable(instance, at) {
			out.ExcludedIncompleteWorkflowCount++
			continue
		}
		baseline, found := workflowRefreshProgressBaselineInstance(instance, baselineAt)
		if !found {
			out.NoBaselineWorkflowCount++
			continue
		}
		if !workflowRefreshProgressHistoryReadable(baseline, baselineAt) {
			out.ExcludedIncompleteWorkflowCount++
			continue
		}
		before, beforeStatus := workflowActiveConditionEvidence(baseline)
		after, afterStatus := workflowActiveConditionEvidence(instance)
		if beforeStatus == "limit" || afterStatus == "limit" {
			out.ExcludedConditionEvidenceWorkflowCount++
			continue
		}
		if beforeStatus == "ambiguous" || afterStatus == "ambiguous" {
			out.ExcludedAmbiguousWorkflowCount++
			continue
		}
		overdue := map[workflowConditionOutcomeKey]api.OperationWorkflowDecisionOutcomeEvidence{}
		if !baseline.Current.Terminal {
			for key, fact := range before {
				workflowEvaluateConditionFreshness(&fact, baselineAt)
				if fact.ConditionFreshness.Status == "overdue" {
					age := workflowConditionEvidenceAge(fact, baselineAt)
					fact.EvidenceAgeSeconds = &age
					overdue[key] = fact
				}
			}
		}
		if len(overdue) == 0 {
			out.NoOverdueBaselineWorkflowCount++
			continue
		}
		// Result changes retain the exact definition identity; omit result from lookup membership.
		current := map[workflowConditionIdentity]api.OperationWorkflowDecisionOutcomeEvidence{}
		for key, fact := range after {
			workflowEvaluateConditionFreshness(&fact, at)
			age := workflowConditionEvidenceAge(fact, at)
			fact.EvidenceAgeSeconds = &age
			current[key.Identity] = fact
		}
		keys := make([]workflowConditionOutcomeKey, 0, len(overdue))
		for key := range overdue {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return workflowConditionOutcomeKeyLess(keys[i], keys[j]) })
		item := api.OperationWorkflowConditionRefreshProgressInstance{PlatformTenantID: instance.Tenant, Subject: instance.Subject, State: instance.Current, BaselineOverdueConditionCount: int64(len(keys)), Changes: []api.OperationWorkflowConditionRefreshProgressChange{}}
		counts := map[string]int64{}
		for _, key := range keys {
			var latest *api.OperationWorkflowDecisionOutcomeEvidence
			if fact, ok := current[key.Identity]; ok {
				value := fact
				latest = &value
			}
			change := workflowRefreshProgressChange(overdue[key], latest, effectiveWorkflowContractVersion(instance.Current.ContractVersion) != key.Identity.Version)
			counts[change.Status]++
			totals[change.Status]++
			if change.NewerReport {
				item.NewerReportConditionCount++
				out.NewerReportConditionCount++
			}
			if len(item.Changes) < api.OperationWorkflowConditionRefreshEvidenceMax {
				item.Changes = append(item.Changes, change)
			} else {
				item.ChangesTruncated = true
			}
		}
		item.Statuses = workflowRefreshProgressCounts(counts)
		out.Items = append(out.Items, item)
		out.ComparedWorkflowCount++
	}
	out.Statuses = workflowRefreshProgressCounts(totals)
	return out
}
func hideRefreshProgressTenants(progress *api.OperationWorkflowConditionRefreshProgress) {
	if progress == nil {
		return
	}
	for i := range progress.Items {
		progress.Items[i].PlatformTenantID = ""
		progress.Items[i].State.PlatformTenantID = ""
	}
}
