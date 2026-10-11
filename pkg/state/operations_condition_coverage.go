package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowConditionSteps(steps []api.OperationWorkflowSpec, workflow string, version int) []api.OperationWorkflowSpec {
	out := []api.OperationWorkflowSpec{}
	for _, step := range steps {
		if step.Workflow == workflow && effectiveWorkflowContractVersion(step.Version) == effectiveWorkflowContractVersion(version) {
			out = append(out, step)
		}
	}
	return out
}
func workflowReportConditionSteps(steps []api.OperationWorkflowSpec, milestones map[string]operationMilestoneReceipt, history api.OperationWorkflowStateHistoryEntry) map[string]string {
	out := map[string]string{}
	for id := range workflowReportConditionDefinitions(steps, milestones, history) {
		for _, step := range workflowConditionSteps(steps, history.Workflow, history.ContractVersion) {
			if step.Milestone == milestones[id].Milestone.Name {
				out[id] = step.Step
			}
		}
	}
	return out
}
func workflowConditionExpectations(instance workflowPerformanceInstance) []api.OperationWorkflowConditionCoverageEntry {
	out := []api.OperationWorkflowConditionCoverageEntry{}
	for _, step := range instance.CurrentConditionSteps {
		for _, definition := range step.DecisionConditions {
			for _, state := range definition.ExpectedStates {
				if state == instance.Current.State {
					out = append(out, api.OperationWorkflowConditionCoverageEntry{Step: step.Step, Milestone: step.Milestone, Definition: definition, Status: "missing"})
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Step != out[j].Step {
			return out[i].Step < out[j].Step
		}
		return out[i].Definition.ID < out[j].Definition.ID
	})
	return out
}

type workflowCoverageConditionKey struct{ Step, ID, Description string }

// Preserve step identity even when two steps declare the same condition vocabulary.
func workflowExpectedConditionEvidence(instance workflowPerformanceInstance, at time.Time) (map[workflowCoverageConditionKey]api.OperationWorkflowDecisionOutcomeEvidence, string) {
	contexts, declarations, version, status := workflowActiveConditionContexts(instance)
	out := map[workflowCoverageConditionKey]api.OperationWorkflowDecisionOutcomeEvidence{}
	if status != "" {
		return nil, status
	}
	if version != effectiveWorkflowContractVersion(instance.Current.ContractVersion) {
		return out, ""
	}
	sourceSteps := map[string]map[string]string{}
	for _, observation := range instance.Observations {
		sourceSteps[observation.History.ID] = observation.ConditionSteps
	}
	for context, fact := range contexts {
		step := sourceSteps[fact.ReportID][fact.MilestoneID]
		if step == "" {
			continue
		}
		for _, result := range fact.Decision.Conditions {
			key := workflowCoverageConditionKey{Step: step, ID: result.ID, Description: result.Description}
			previous, exists := out[key]
			if exists && previous.Condition.Result != result.Result {
				return nil, "ambiguous"
			}
			if !exists || fact.Revision > previous.Revision || fact.Revision == previous.Revision && (fact.ReportID < previous.ReportID || fact.ReportID == previous.ReportID && fact.MilestoneID < previous.MilestoneID) {
				value, condition := fact, result
				value.Condition = &condition
				value.ConditionFreshness = workflowConditionFreshnessDefinition(declarations[context], result)
				workflowEvaluateConditionFreshness(&value, at)
				age := workflowConditionEvidenceAge(value, at)
				value.EvidenceAgeSeconds = &age
				out[key] = value
			}
			if len(out) > api.OperationWorkflowConditionIdentitiesPerWorkflowMax {
				return nil, "limit"
			}
		}
	}
	return out, ""
}
func workflowConditionCoverage(instances []workflowPerformanceInstance, at time.Time) *api.OperationWorkflowConditionCoverage {
	out := &api.OperationWorkflowConditionCoverage{SampledWorkflowCount: int64(len(instances)), Items: []api.OperationWorkflowConditionCoverageInstance{}}
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.CohortTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	ordered := append([]workflowPerformanceInstance(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		return operationAttentionKey(a.Tenant, a.Subject, a.Current.Workflow, a.Current.InstanceID) < operationAttentionKey(b.Tenant, b.Subject, b.Current.Workflow, b.Current.InstanceID)
	})
	buckets := map[workflowMissingConditionGroupKey]*workflowMissingConditionBucket{}
	for _, instance := range ordered {
		item, status := workflowConditionCoverageInstance(instance, at)
		switch status {
		case "unconfigured":
			out.NoExpectationsWorkflowCount++
			continue
		case "incomplete":
			out.ExcludedIncompleteWorkflowCount++
			continue
		case "limit":
			out.ExcludedConditionEvidenceWorkflowCount++
			continue
		case "ambiguous":
			out.ExcludedAmbiguousWorkflowCount++
			continue
		}
		workflowMissingConditionBuckets(instance, item, buckets)
		if len(item.Conditions) > api.OperationWorkflowConditionRefreshEvidenceMax {
			item.Conditions = item.Conditions[:api.OperationWorkflowConditionRefreshEvidenceMax]
			item.ConditionsTruncated = true
		}
		out.ComparedWorkflowCount++
		out.ExpectedConditionCount += item.ExpectedConditionCount
		out.ReportedConditionCount += item.ReportedConditionCount
		out.MissingConditionCount += item.MissingConditionCount
		out.OverdueConditionCount += item.OverdueConditionCount
		out.Items = append(out.Items, item)
	}
	workflowMissingConditionGroups(out, buckets)
	return out
}
func workflowConditionCoverageInstance(instance workflowPerformanceInstance, at time.Time) (api.OperationWorkflowConditionCoverageInstance, string) {
	expectations := workflowConditionExpectations(instance)
	if len(expectations) == 0 {
		return api.OperationWorkflowConditionCoverageInstance{}, "unconfigured"
	}
	if instance.Current.Terminal || instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
		return api.OperationWorkflowConditionCoverageInstance{}, "incomplete"
	}
	evidence, status := workflowExpectedConditionEvidence(instance, at)
	if len(expectations) > api.OperationWorkflowConditionIdentitiesPerWorkflowMax || status == "limit" {
		return api.OperationWorkflowConditionCoverageInstance{}, "limit"
	}
	if status == "ambiguous" {
		return api.OperationWorkflowConditionCoverageInstance{}, "ambiguous"
	}
	item := api.OperationWorkflowConditionCoverageInstance{PlatformTenantID: instance.Tenant, Subject: instance.Subject, State: instance.Current, ExpectedConditionCount: int64(len(expectations)), Conditions: []api.OperationWorkflowConditionCoverageEntry{}}
	for _, entry := range expectations {
		if fact, exists := evidence[workflowCoverageConditionKey{Step: entry.Step, ID: entry.Definition.ID, Description: entry.Definition.Description}]; exists {
			value := fact
			entry.Evidence = &value
		}
		if entry.Evidence == nil {
			item.MissingConditionCount++
		} else {
			item.ReportedConditionCount++
			entry.Status = "reported"
			if entry.Evidence.ConditionFreshness.Status == "overdue" {
				item.OverdueConditionCount++
				entry.Status = "overdue"
			}
		}
		item.Conditions = append(item.Conditions, entry)
	}
	return item, ""
}

func hideConditionCoverageTenants(coverage *api.OperationWorkflowConditionCoverage) {
	if coverage == nil {
		return
	}
	for i := range coverage.Items {
		coverage.Items[i].PlatformTenantID = ""
		coverage.Items[i].State.PlatformTenantID = ""
	}
}
