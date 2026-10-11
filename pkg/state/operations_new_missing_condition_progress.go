package state

import (
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowNewMissingProgressCounts(counts map[string]int64) []api.OperationWorkflowNewMissingConditionProgressCount {
	out := []api.OperationWorkflowNewMissingConditionProgressCount{}
	for _, status := range []string{"report_lost", "new_expectation", "still_missing", "expectation_changed", "contract_changed"} {
		out = append(out, api.OperationWorkflowNewMissingConditionProgressCount{Status: status, ConditionCount: counts[status]})
	}
	return out
}
func workflowNewMissingProgressChange(before *api.OperationWorkflowConditionCoverageEntry, after api.OperationWorkflowConditionCoverageEntry, contractChanged bool) api.OperationWorkflowNewMissingConditionProgressChange {
	out := api.OperationWorkflowNewMissingConditionProgressChange{Before: before, After: after}
	switch {
	case contractChanged:
		out.Status = "contract_changed"
		out.Before = nil
		out.Explanation = "The current missing expectation belongs to a changed workflow contract. It is not compared with baseline evidence across contracts."
	case before == nil:
		out.Status = "new_expectation"
		out.Explanation = "This step and condition were not active expectations in the baseline state, or the baseline workflow was terminal. The current missing report is a new expectation, not lost evidence."
	case before.Milestone != after.Milestone || !reflect.DeepEqual(before.Definition, after.Definition):
		out.Status = "expectation_changed"
		out.Explanation = "The pinned declaration or milestone changed. The current gap is separate from the prior expectation and does not establish lost evidence."
	case before.Evidence != nil:
		out.Status = "report_lost"
		out.Explanation = "The identical active expectation had a report at baseline and now lacks matching evidence in latest explicit contexts. This is reporting loss, not a failed business check; freshness expiry alone does not remove reports."
	default:
		out.Status = "still_missing"
		out.Explanation = "The identical active expectation was missing at baseline and remains missing. It is not a newly introduced gap."
	}
	return out
}
func workflowNewMissingConditionProgress(instances []workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time) *api.OperationWorkflowNewMissingConditionProgress {
	out := &api.OperationWorkflowNewMissingConditionProgress{BaselineAt: baselineAt, EvaluatedAt: at, SampledWorkflowCount: int64(len(instances)), Items: []api.OperationWorkflowNewMissingConditionProgressInstance{}}
	totals := map[string]int64{}
	ordered := append([]workflowPerformanceInstance(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return operationAttentionKey(a.Tenant, a.Subject, a.Current.Workflow, a.Current.InstanceID) < operationAttentionKey(b.Tenant, b.Subject, b.Current.Workflow, b.Current.InstanceID)
	})
	buckets := map[workflowGapProgressGroupKey]*workflowMissingConditionBucket{}
	for _, instance := range ordered {
		item, status := workflowNewMissingProgressInstance(instance, definitions, baselineAt, at)
		switch status {
		case "incomplete":
			out.ExcludedIncompleteWorkflowCount++
			continue
		case "no_baseline":
			out.NoBaselineWorkflowCount++
			continue
		case "definition":
			out.ExcludedDefinitionWorkflowCount++
			continue
		case "limit":
			out.ExcludedConditionEvidenceWorkflowCount++
			continue
		case "ambiguous":
			out.ExcludedAmbiguousWorkflowCount++
			continue
		case "no_gap":
			out.NoCurrentMissingWorkflowCount++
			continue
		}
		for _, count := range item.Statuses {
			totals[count.Status] += count.ConditionCount
		}
		workflowGapProgressBuckets(instance, item, buckets, at)
		if len(item.Changes) > api.OperationWorkflowConditionRefreshEvidenceMax {
			item.Changes = item.Changes[:api.OperationWorkflowConditionRefreshEvidenceMax]
			item.ChangesTruncated = true
		}
		out.Items = append(out.Items, item)
		out.ComparedWorkflowCount++
	}
	out.Statuses = workflowNewMissingProgressCounts(totals)
	workflowGapProgressGroups(out, buckets)
	return out
}
func hideNewMissingProgressTenants(progress *api.OperationWorkflowNewMissingConditionProgress) {
	if progress == nil {
		return
	}
	for i := range progress.Items {
		progress.Items[i].PlatformTenantID = ""
		progress.Items[i].State.PlatformTenantID = ""
	}
}

func workflowNewMissingProgressInstance(instance workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time) (api.OperationWorkflowNewMissingConditionProgressInstance, string) {
	baseline, before, after, status := workflowMissingProgressComparison(instance, definitions, baselineAt, at)
	if status != "" {
		return api.OperationWorkflowNewMissingConditionProgressInstance{}, status
	}
	missing := []api.OperationWorkflowConditionCoverageEntry{}
	if !instance.Current.Terminal {
		for _, entry := range after {
			if entry.Status == "missing" {
				missing = append(missing, entry)
			}
		}
	}
	if len(missing) == 0 {
		return api.OperationWorkflowNewMissingConditionProgressInstance{}, "no_gap"
	}
	type key struct{ Step, ID string }
	previous := map[key]api.OperationWorkflowConditionCoverageEntry{}
	if !baseline.Current.Terminal {
		for _, entry := range before {
			previous[key{entry.Step, entry.Definition.ID}] = entry
		}
	}
	item := api.OperationWorkflowNewMissingConditionProgressInstance{PlatformTenantID: instance.Tenant, Subject: instance.Subject, BaselineReport: baseline.Observations[len(baseline.Observations)-1].History, BaselineTerminal: baseline.Current.Terminal, State: instance.Current, StateChanged: baseline.Current.State != instance.Current.State, CurrentMissingConditionCount: int64(len(missing)), Changes: []api.OperationWorkflowNewMissingConditionProgressChange{}}
	counts := map[string]int64{}
	for _, entry := range missing {
		var old *api.OperationWorkflowConditionCoverageEntry
		if value, exists := previous[key{entry.Step, entry.Definition.ID}]; exists {
			v := value
			old = &v
		}
		change := workflowNewMissingProgressChange(old, entry, effectiveWorkflowContractVersion(baseline.Current.ContractVersion) != effectiveWorkflowContractVersion(instance.Current.ContractVersion))
		counts[change.Status]++
		item.Changes = append(item.Changes, change)
	}
	item.Statuses = workflowNewMissingProgressCounts(counts)
	return item, ""
}
