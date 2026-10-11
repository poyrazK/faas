package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Select snapshots before extracting conditions: explicit omission cannot resurrect an old result.
// Reasons are one context; each selected path group is another. Contract changes reset all contexts.
func workflowActiveConditionEvidence(instance workflowPerformanceInstance) (map[workflowConditionOutcomeKey]api.OperationWorkflowDecisionOutcomeEvidence, string) {
	contexts := map[string]api.OperationWorkflowDecisionOutcomeEvidence{}
	declarations := map[string][]api.OperationBusinessConditionDefinition{}
	version := 0
	for _, observation := range workflowDecisionOutcomeReports(instance) {
		h := observation.History
		currentVersion := effectiveWorkflowContractVersion(h.ContractVersion)
		if currentVersion != version {
			contexts = map[string]api.OperationWorkflowDecisionOutcomeEvidence{}
			version = currentVersion
			declarations = map[string][]api.OperationBusinessConditionDefinition{}
		}
		fact := api.OperationWorkflowDecisionOutcomeEvidence{ReportID: h.ID, OperationID: h.OperationID, Revision: h.Revision, FromState: h.FromState, State: h.State, OccurredAt: h.OccurredAt}
		if h.ChangeReason != nil {
			decision := api.CloneOperationBusinessDecision(h.ChangeReason.Decision)
			value := fact
			value.Decision = &decision
			value.MilestoneID = h.ChangeReason.MilestoneID
			contexts["reason"] = value
			declarations["reason"] = observation.ConditionDefinitions[value.MilestoneID]
		}
		// A reported path snapshot replaces its groups, including paths without a decision fact.
		if len(h.SelectedPaths) != 0 {
			for context := range contexts {
				if context != "reason" {
					delete(contexts, context)
					delete(declarations, context)
				}
			}
			for _, path := range h.SelectedPaths {
				if path.DecisionMilestoneID == "" {
					continue
				}
				value := fact
				value.MilestoneID = path.DecisionMilestoneID
				value.Decision = &api.OperationBusinessDecision{Workflow: h.Workflow, InstanceID: h.InstanceID, Code: path.DecisionCode, Description: path.DecisionDescription, RuleID: path.DecisionRuleID, RuleVersion: path.DecisionRuleVersion, Conditions: append([]api.OperationBusinessConditionResult(nil), path.DecisionConditions...)}
				contexts["path:"+path.GroupID] = value
				declarations["path:"+path.GroupID] = observation.ConditionDefinitions[value.MilestoneID]
			}
		}
		if len(contexts) > api.OperationWorkflowActiveConditionContextsMax {
			return nil, "limit"
		}
	}
	out := map[workflowConditionOutcomeKey]api.OperationWorkflowDecisionOutcomeEvidence{}
	if version != effectiveWorkflowContractVersion(instance.Current.ContractVersion) {
		return out, ""
	}
	results := map[workflowConditionIdentity]string{}
	contextKeys := make([]string, 0, len(contexts))
	for context := range contexts {
		contextKeys = append(contextKeys, context)
	}
	sort.Strings(contextKeys)
	for _, context := range contextKeys {
		fact := contexts[context]
		for _, condition := range fact.Decision.Conditions {
			identity := workflowConditionIdentity{Version: version, Code: fact.Decision.Code, Rule: fact.Decision.RuleID, RuleVersion: fact.Decision.RuleVersion, ID: condition.ID, Description: condition.Description}
			if result, exists := results[identity]; exists && result != condition.Result {
				return nil, "ambiguous"
			}
			results[identity] = condition.Result
			if len(results) > api.OperationWorkflowConditionIdentitiesPerWorkflowMax {
				return nil, "limit"
			}
			key := workflowConditionOutcomeKey{Identity: identity, Result: condition.Result}
			previous, exists := out[key]
			if !exists || fact.Revision > previous.Revision || fact.Revision == previous.Revision && (fact.ReportID < previous.ReportID || fact.ReportID == previous.ReportID && fact.MilestoneID < previous.MilestoneID) {
				value := fact
				c := condition
				value.Condition = &c
				value.ConditionFreshness = workflowConditionFreshnessDefinition(declarations[context], c)
				out[key] = value
			}
		}
	}
	return out, ""
}

type workflowActiveConditionBucket struct {
	Group                 api.OperationWorkflowActiveConditionGroup
	Elapsed, Blocked, Age []int64
}

func workflowActiveConditions(instances []workflowPerformanceInstance, at time.Time) *api.OperationWorkflowActiveConditionSummary {
	out := &api.OperationWorkflowActiveConditionSummary{SampledWorkflowCount: int64(len(instances)), Groups: []api.OperationWorkflowActiveConditionGroup{}}
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.CohortTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	buckets := map[workflowConditionOutcomeKey]*workflowActiveConditionBucket{}
	for _, instance := range instances {
		if instance.Current.Terminal || instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
			out.ExcludedIncompleteWorkflowCount++
			continue
		}
		evidence, status := workflowActiveConditionEvidence(instance)
		if status == "limit" {
			out.ExcludedConditionEvidenceWorkflowCount++
			continue
		}
		if status == "ambiguous" {
			out.ExcludedAmbiguousWorkflowCount++
			continue
		}
		if len(evidence) == 0 {
			out.UnclassifiedWorkflowCount++
			continue
		}
		out.LinkedWorkflowCount++
		for key, fact := range evidence {
			workflowEvaluateConditionFreshness(&fact, at)
			bucket := buckets[key]
			if bucket == nil {
				if len(buckets) == api.OperationWorkflowActiveConditionGroupsMax {
					out.GroupsTruncated = true
					var largest workflowConditionOutcomeKey
					first := true
					for existing := range buckets {
						if first || workflowConditionOutcomeKeyLess(largest, existing) {
							largest = existing
							first = false
						}
					}
					if !workflowConditionOutcomeKeyLess(key, largest) {
						continue
					}
					delete(buckets, largest)
				}
				id := key.Identity
				bucket = &workflowActiveConditionBucket{Group: api.OperationWorkflowActiveConditionGroup{ContractVersion: id.Version, DecisionCode: id.Code, RuleID: id.Rule, RuleVersion: id.RuleVersion, ConditionID: id.ID, ConditionDescription: id.Description, ConditionResult: key.Result}}
				buckets[key] = bucket
			}
			bucket.Elapsed = append(bucket.Elapsed, instance.Bottlenecks.StateSeconds)
			bucket.Blocked = append(bucket.Blocked, instance.Bottlenecks.BlockedSeconds)
			bucket.Age = append(bucket.Age, workflowConditionEvidenceAge(fact, at))
			switch fact.ConditionFreshness.Status {
			case "within_budget":
				bucket.Group.WithinBudgetWorkflowCount++
			case "overdue":
				bucket.Group.OverdueWorkflowCount++
			case "no_budget":
				bucket.Group.NoBudgetWorkflowCount++
			}
			if instance.Current.Stale {
				bucket.Group.StaleWorkflowCount++
			}
		}
	}
	keys := make([]workflowConditionOutcomeKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return workflowConditionOutcomeKeyLess(keys[i], keys[j]) })
	for _, key := range keys {
		bucket := buckets[key]
		bucket.Group.ElapsedTime = workflowDurationDistribution(bucket.Elapsed)
		bucket.Group.BlockedTime = workflowDurationDistribution(bucket.Blocked)
		bucket.Group.EvidenceAge = workflowDurationDistribution(bucket.Age)
		out.Groups = append(out.Groups, bucket.Group)
	}
	workflowConditionRefreshOwners(instances, at, out)
	return out
}
func workflowConditionEvidenceAge(fact api.OperationWorkflowDecisionOutcomeEvidence, at time.Time) int64 {
	seconds := int64(at.Sub(fact.OccurredAt) / time.Second)
	if seconds < 0 {
		return 0
	}
	return seconds
}
func activeConditionDimension(d string) bool {
	return d == "active_condition_elapsed" || d == "active_condition_blocked"
}
func validateActiveConditionGroup(g api.OperationWorkflowPerformanceGroup) error {
	if g.OutcomeCode != "" {
		return ErrInvalidArgument
	}
	if g.ConditionEvidenceStatus != "" && g.ConditionEvidenceStatus != "within_budget" && g.ConditionEvidenceStatus != "overdue" && g.ConditionEvidenceStatus != "no_budget" {
		return ErrInvalidArgument
	}
	g.ConditionEvidenceStatus = ""
	return validateConditionGroupSelectors(g)
}
func workflowActiveConditionGroupKey(g api.OperationWorkflowActiveConditionGroup) workflowConditionOutcomeKey {
	return workflowConditionOutcomeSelector(api.OperationWorkflowPerformanceGroup{ContractVersion: g.ContractVersion, DecisionCode: g.DecisionCode, RuleID: g.RuleID, RuleVersion: g.RuleVersion, ConditionID: g.ConditionID, ConditionDescription: g.ConditionDescription, ConditionResult: g.ConditionResult})
}
func workflowActiveConditionContribution(instance workflowPerformanceInstance, group api.OperationWorkflowPerformanceGroup, at time.Time) (int64, *api.OperationWorkflowDecisionOutcomeEvidence) {
	facts, status := workflowActiveConditionEvidence(instance)
	if status != "" {
		return 0, nil
	}
	fact, exists := facts[workflowConditionOutcomeSelector(group)]
	if !exists {
		return 0, nil
	}
	workflowEvaluateConditionFreshness(&fact, at)
	if group.ConditionEvidenceStatus != "" && group.ConditionEvidenceStatus != fact.ConditionFreshness.Status {
		return 0, nil
	}
	age := workflowConditionEvidenceAge(fact, at)
	fact.EvidenceAgeSeconds = &age
	if group.Dimension == "active_condition_blocked" {
		return instance.Bottlenecks.BlockedSeconds, &fact
	}
	return instance.Bottlenecks.StateSeconds, &fact
}
