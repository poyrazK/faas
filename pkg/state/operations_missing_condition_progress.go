package state

import (
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowMissingProgressOperations(instances []workflowPerformanceInstance, at time.Time) []string {
	ids := map[string]bool{}
	for _, instance := range instances {
		if baseline, found := workflowRefreshProgressBaselineInstance(instance, at); found {
			ids[baseline.Current.OperationID] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func workflowMissingProgressCounts(counts map[string]int64) []api.OperationWorkflowMissingConditionProgressCount {
	out := []api.OperationWorkflowMissingConditionProgressCount{}
	for _, status := range []string{"received_report", "still_missing", "expectation_changed", "not_expected", "contract_changed"} {
		out = append(out, api.OperationWorkflowMissingConditionProgressCount{Status: status, ConditionCount: counts[status]})
	}
	return out
}
func workflowMissingProgressEntries(instance workflowPerformanceInstance, at time.Time) ([]api.OperationWorkflowConditionCoverageEntry, string) {
	expectations := workflowConditionExpectations(instance)
	evidence, status := workflowExpectedConditionEvidence(instance, at)
	if len(expectations) > api.OperationWorkflowConditionIdentitiesPerWorkflowMax || status == "limit" {
		return nil, "limit"
	}
	if status != "" {
		return nil, status
	}
	for i := range expectations {
		entry := &expectations[i]
		if fact, exists := evidence[workflowCoverageConditionKey{Step: entry.Step, ID: entry.Definition.ID, Description: entry.Definition.Description}]; exists {
			value := fact
			entry.Evidence = &value
			entry.Status = "reported"
			if value.ConditionFreshness.Status == "overdue" {
				entry.Status = "overdue"
			}
		}
	}
	return expectations, ""
}
func workflowMissingProgressChange(before api.OperationWorkflowConditionCoverageEntry, after *api.OperationWorkflowConditionCoverageEntry, contractChanged bool) api.OperationWorkflowMissingConditionProgressChange {
	out := api.OperationWorkflowMissingConditionProgressChange{Before: before, After: after}
	switch {
	case contractChanged:
		out.Status = "contract_changed"
		out.After = nil
		out.Explanation = "The workflow contract changed; baseline expectations are not compared across contracts."
	case after == nil:
		out.Status = "not_expected"
		out.Explanation = "This step and condition are no longer expected in the current state and pinned definition. This does not prove a resolved business check."
	case before.Milestone != after.Milestone || !reflect.DeepEqual(before.Definition, after.Definition):
		out.Status = "expectation_changed"
		out.Explanation = "The pinned condition declaration or milestone changed. Current evidence is shown separately and is not counted as filling the old expectation."
	case after.Evidence != nil:
		out.Status = "received_report"
		out.Explanation = "The missing expectation now has matching evidence in the latest explicit contexts. Any reported result qualifies; the report may already be overdue and does not establish successful verification."
	default:
		out.Status = "still_missing"
		out.Explanation = "The same pinned expectation remains active with no matching evidence in the latest explicit contexts."
	}
	return out
}
func workflowMissingConditionProgress(instances []workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time) *api.OperationWorkflowMissingConditionProgress {
	out := &api.OperationWorkflowMissingConditionProgress{BaselineAt: baselineAt, EvaluatedAt: at, SampledWorkflowCount: int64(len(instances)), Items: []api.OperationWorkflowMissingConditionProgressInstance{}}
	totals := map[string]int64{}
	ordered := append([]workflowPerformanceInstance(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return operationAttentionKey(a.Tenant, a.Subject, a.Current.Workflow, a.Current.InstanceID) < operationAttentionKey(b.Tenant, b.Subject, b.Current.Workflow, b.Current.InstanceID)
	})
	for _, instance := range ordered {
		baseline, before, after, status := workflowMissingProgressComparison(instance, definitions, baselineAt, at)
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
		}
		missing := []api.OperationWorkflowConditionCoverageEntry{}
		if !baseline.Current.Terminal {
			for _, entry := range before {
				if entry.Status == "missing" {
					missing = append(missing, entry)
				}
			}
		}
		if len(missing) == 0 {
			out.NoMissingBaselineWorkflowCount++
			continue
		}
		// Step and ID find replacements; definition changes remain explicit rather than receiving old gaps.
		type key struct{ Step, ID string }
		current := map[key]api.OperationWorkflowConditionCoverageEntry{}
		for _, entry := range after {
			current[key{entry.Step, entry.Definition.ID}] = entry
		}
		item := api.OperationWorkflowMissingConditionProgressInstance{PlatformTenantID: instance.Tenant, Subject: instance.Subject, BaselineReport: baseline.Observations[len(baseline.Observations)-1].History, State: instance.Current, StateChanged: baseline.Current.State != instance.Current.State, BaselineMissingConditionCount: int64(len(missing)), Changes: []api.OperationWorkflowMissingConditionProgressChange{}}
		counts := map[string]int64{}
		for _, entry := range missing {
			var latest *api.OperationWorkflowConditionCoverageEntry
			if value, exists := current[key{entry.Step, entry.Definition.ID}]; exists {
				v := value
				latest = &v
			}
			change := workflowMissingProgressChange(entry, latest, effectiveWorkflowContractVersion(baseline.Current.ContractVersion) != effectiveWorkflowContractVersion(instance.Current.ContractVersion))
			counts[change.Status]++
			totals[change.Status]++
			if len(item.Changes) < api.OperationWorkflowConditionRefreshEvidenceMax {
				item.Changes = append(item.Changes, change)
			} else {
				item.ChangesTruncated = true
			}
		}
		item.Statuses = workflowMissingProgressCounts(counts)
		out.Items = append(out.Items, item)
		out.ComparedWorkflowCount++
	}
	out.Statuses = workflowMissingProgressCounts(totals)
	return out
}
func hideMissingProgressTenants(progress *api.OperationWorkflowMissingConditionProgress) {
	if progress == nil {
		return
	}
	for i := range progress.Items {
		item := &progress.Items[i]
		item.PlatformTenantID = ""
		item.State.PlatformTenantID = ""
	}
}

// Both progress views use publication visibility, retained definitions and full evidence before preview caps.
func workflowMissingProgressComparison(instance workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time) (workflowPerformanceInstance, []api.OperationWorkflowConditionCoverageEntry, []api.OperationWorkflowConditionCoverageEntry, string) {
	if !workflowRefreshProgressHistoryReadable(instance, at) {
		return workflowPerformanceInstance{}, nil, nil, "incomplete"
	}
	baseline, found := workflowRefreshProgressBaselineInstance(instance, baselineAt)
	if !found {
		return baseline, nil, nil, "no_baseline"
	}
	if !calculateWorkflowBottlenecks(baseline.Observations, &baseline.Current, nil, baselineAt, false).HistoryComplete {
		return baseline, nil, nil, "incomplete"
	}
	steps, found := definitions[baseline.Current.OperationID]
	if !found {
		return baseline, nil, nil, "definition"
	}
	baseline.CurrentConditionSteps = workflowConditionSteps(steps, baseline.Current.Workflow, baseline.Current.ContractVersion)
	before, beforeStatus := workflowMissingProgressEntries(baseline, baselineAt)
	after, afterStatus := workflowMissingProgressEntries(instance, at)
	if beforeStatus == "limit" || afterStatus == "limit" {
		return baseline, nil, nil, "limit"
	}
	if beforeStatus == "ambiguous" || afterStatus == "ambiguous" {
		return baseline, nil, nil, "ambiguous"
	}
	return baseline, before, after, ""
}
