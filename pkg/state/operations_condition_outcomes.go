package state

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowConditionIdentity struct {
	Version                                  int
	Code, Rule, RuleVersion, ID, Description string
}
type workflowConditionOutcomeKey struct {
	Identity        workflowConditionIdentity
	Result, Outcome string
}
type workflowConditionObservation struct {
	Results map[string]api.OperationWorkflowDecisionOutcomeEvidence
}
type workflowConditionOutcomeBucket struct {
	Group               api.OperationWorkflowConditionOutcomeGroup
	Completion, Blocked []int64
}

// Conditions are explicit snapshots from reasons and selected-path facts. Omission is not unknown.
// The per-workflow identity cap is enforced before any group display cap; overflowing runs are excluded.
func workflowConditionObservations(instance workflowPerformanceInstance) (map[workflowConditionIdentity]*workflowConditionObservation, bool) {
	out := map[workflowConditionIdentity]*workflowConditionObservation{}
	limited := false
	visitWorkflowDecisionOutcomes(instance, func(key workflowDecisionOutcomeKey, fact api.OperationWorkflowDecisionOutcomeEvidence) bool {
		if key.Kind != "decision" || fact.Decision == nil {
			return true
		}
		for _, c := range fact.Decision.Conditions {
			identity := workflowConditionIdentity{Version: key.Version, Code: key.Code, Rule: key.Rule, RuleVersion: key.RuleVersion, ID: c.ID, Description: c.Description}
			bucket := out[identity]
			if bucket == nil {
				if len(out) == api.OperationWorkflowConditionIdentitiesPerWorkflowMax {
					limited = true
					return false
				}
				bucket = &workflowConditionObservation{Results: map[string]api.OperationWorkflowDecisionOutcomeEvidence{}}
				out[identity] = bucket
			}
			if _, exists := bucket.Results[c.Result]; !exists {
				value := fact
				condition := c
				value.Condition = &condition
				bucket.Results[c.Result] = value
			}
		}
		return true
	})
	if limited {
		return nil, true
	}
	return out, false
}
func workflowConditionOutcomes(instances []workflowPerformanceInstance) *api.OperationWorkflowConditionOutcomeSummary {
	out := &api.OperationWorkflowConditionOutcomeSummary{SampledWorkflowCount: int64(len(instances)), Groups: []api.OperationWorkflowConditionOutcomeGroup{}}
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.CohortTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	buckets := map[workflowConditionOutcomeKey]*workflowConditionOutcomeBucket{}
	for _, instance := range instances {
		if instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
			out.ExcludedIncompleteWorkflowCount++
			continue
		}
		if instance.Current.OutcomeCode == "" {
			out.MissingOutcomeWorkflowCount++
			continue
		}
		observations, limited := workflowConditionObservations(instance)
		if limited {
			out.ExcludedConditionEvidenceWorkflowCount++
			continue
		}
		if len(observations) == 0 {
			out.UnclassifiedWorkflowCount++
			continue
		}
		out.LinkedWorkflowCount++
		changing := false
		for identity, observation := range observations {
			multiple := len(observation.Results) > 1
			changing = changing || multiple
			for result := range observation.Results {
				key := workflowConditionOutcomeKey{Identity: identity, Result: result, Outcome: instance.Current.OutcomeCode}
				bucket := buckets[key]
				if bucket == nil {
					if len(buckets) == api.OperationWorkflowConditionOutcomeGroupsMax {
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
						// The threshold only decreases, so an evicted identity can never re-enter.
						delete(buckets, largest)
					}
					bucket = &workflowConditionOutcomeBucket{Group: api.OperationWorkflowConditionOutcomeGroup{ContractVersion: identity.Version, DecisionCode: identity.Code, RuleID: identity.Rule, RuleVersion: identity.RuleVersion, ConditionID: identity.ID, ConditionDescription: identity.Description, ConditionResult: result, OutcomeCode: key.Outcome}}
					buckets[key] = bucket
				}
				bucket.Completion = append(bucket.Completion, instance.Bottlenecks.StateSeconds)
				bucket.Blocked = append(bucket.Blocked, instance.Bottlenecks.BlockedSeconds)
				if multiple {
					bucket.Group.ChangingResultWorkflowCount++
				}
			}
		}
		if changing {
			out.ChangingResultWorkflowCount++
		}
	}
	keys := make([]workflowConditionOutcomeKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return workflowConditionOutcomeKeyLess(keys[i], keys[j]) })
	for _, key := range keys {
		bucket := buckets[key]
		bucket.Group.CompletionTime = workflowDurationDistribution(bucket.Completion)
		bucket.Group.BlockedTime = workflowDurationDistribution(bucket.Blocked)
		out.Groups = append(out.Groups, bucket.Group)
	}
	return out
}
func workflowConditionOutcomeKeyLess(a, b workflowConditionOutcomeKey) bool {
	if a.Identity.Version != b.Identity.Version {
		return a.Identity.Version < b.Identity.Version
	}
	left := [...]string{a.Identity.Code, a.Identity.Rule, a.Identity.RuleVersion, a.Identity.ID, a.Identity.Description, a.Result, a.Outcome}
	right := [...]string{b.Identity.Code, b.Identity.Rule, b.Identity.RuleVersion, b.Identity.ID, b.Identity.Description, b.Result, b.Outcome}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}
func conditionOutcomeDimension(d string) bool {
	return d == "condition_completion" || d == "condition_blocked"
}
func businessOutcomeDimension(d string) bool {
	return decisionOutcomeDimension(d) || conditionOutcomeDimension(d)
}
func workflowConditionOutcomeSelector(g api.OperationWorkflowPerformanceGroup) workflowConditionOutcomeKey {
	return workflowConditionOutcomeKey{Identity: workflowConditionIdentity{Version: g.ContractVersion, Code: g.DecisionCode, Rule: g.RuleID, RuleVersion: g.RuleVersion, ID: g.ConditionID, Description: g.ConditionDescription}, Result: g.ConditionResult, Outcome: g.OutcomeCode}
}
func workflowConditionOutcomeGroupKey(g api.OperationWorkflowConditionOutcomeGroup) workflowConditionOutcomeKey {
	return workflowConditionOutcomeSelector(api.OperationWorkflowPerformanceGroup{ContractVersion: g.ContractVersion, DecisionCode: g.DecisionCode, RuleID: g.RuleID, RuleVersion: g.RuleVersion, ConditionID: g.ConditionID, ConditionDescription: g.ConditionDescription, ConditionResult: g.ConditionResult, OutcomeCode: g.OutcomeCode})
}
func validateConditionOutcomeGroup(g api.OperationWorkflowPerformanceGroup) error {
	if api.ValidateOperationWorkflowStateName(g.OutcomeCode) != nil {
		return ErrInvalidArgument
	}
	return validateConditionGroupSelectors(g)
}
func validateConditionGroupSelectors(g api.OperationWorkflowPerformanceGroup) error {
	if g.ConditionEvidenceStatus != "" || g.ContractVersion < 1 || g.ContractVersion > api.OperationWorkflowContractVersionMax || g.State != "" || g.Operation != "" || g.Code != "" || g.Owner != "" || g.Unassigned || g.Kind != "" || g.GroupID != "" || g.PathID != "" {
		return ErrInvalidArgument
	}
	if api.ValidateOperationBusinessDecision(api.OperationBusinessDecision{Workflow: "workflow", InstanceID: "instance", Code: g.DecisionCode, Description: "decision", RuleID: g.RuleID, RuleVersion: g.RuleVersion}) != nil {
		return ErrInvalidArgument
	}
	if _, err := api.CanonicalOperationBusinessConditionResults([]api.OperationBusinessConditionResult{{ID: g.ConditionID, Description: g.ConditionDescription, Result: g.ConditionResult}}); err != nil {
		return ErrInvalidArgument
	}
	return nil
}
func workflowConditionOutcomeContribution(instance workflowPerformanceInstance, g api.OperationWorkflowPerformanceGroup) (int64, *api.OperationWorkflowDecisionOutcomeEvidence) {
	if instance.Current.OutcomeCode != g.OutcomeCode {
		return 0, nil
	}
	observations, limited := workflowConditionObservations(instance)
	if limited {
		return 0, nil
	}
	key := workflowConditionOutcomeSelector(g)
	observation := observations[key.Identity]
	if observation == nil {
		return 0, nil
	}
	evidence, exists := observation.Results[key.Result]
	if !exists {
		return 0, nil
	}
	for result := range observation.Results {
		evidence.ObservedConditionResults = append(evidence.ObservedConditionResults, result)
	}
	sort.Strings(evidence.ObservedConditionResults)
	if g.Dimension == "condition_blocked" {
		return instance.Bottlenecks.BlockedSeconds, &evidence
	}
	return instance.Bottlenecks.StateSeconds, &evidence
}
