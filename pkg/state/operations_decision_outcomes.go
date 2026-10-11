package state

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowDecisionOutcomeKey struct {
	Version                                             int
	Kind, Code, Rule, RuleVersion, Group, Path, Outcome string
}
type workflowDecisionOutcomeBucket struct {
	Group               api.OperationWorkflowDecisionOutcomeGroup
	Completion, Blocked []int64
}

// Only complete completed histories qualify. Repeated facts count once per workflow/group.
func workflowDecisionOutcomes(instances []workflowPerformanceInstance) *api.OperationWorkflowDecisionOutcomeSummary {
	out := &api.OperationWorkflowDecisionOutcomeSummary{SampledWorkflowCount: int64(len(instances)), Groups: []api.OperationWorkflowDecisionOutcomeGroup{}}
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.CohortTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	buckets := map[workflowDecisionOutcomeKey]*workflowDecisionOutcomeBucket{}
	less := decisionOutcomeKeyLess
	for _, instance := range instances {
		if instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
			out.ExcludedIncompleteWorkflowCount++
			continue
		}
		if instance.Current.OutcomeCode == "" {
			out.MissingOutcomeWorkflowCount++
			continue
		}
		seen := map[workflowDecisionOutcomeKey]bool{}
		observations := append([]workflowBottleneckObservation(nil), instance.Observations...)
		sort.Slice(observations, func(i, j int) bool { return workflowBottleneckHistoryLess(observations[i], observations[j]) })
		add := func(key workflowDecisionOutcomeKey) {
			if seen[key] {
				return
			}
			seen[key] = true
			if buckets[key] == nil {
				if len(buckets) == api.OperationWorkflowDecisionOutcomeGroupsMax {
					out.GroupsTruncated = true
					var largest workflowDecisionOutcomeKey
					first := true
					for existing := range buckets {
						if first || less(largest, existing) {
							largest = existing
							first = false
						}
					}
					if !less(key, largest) {
						return
					}
					// The retention threshold only decreases: an evicted key can never
					// qualify again, so returned groups retain every workflow sample.
					delete(buckets, largest)
				}
				buckets[key] = &workflowDecisionOutcomeBucket{Group: api.OperationWorkflowDecisionOutcomeGroup{Kind: key.Kind, ContractVersion: key.Version, DecisionCode: key.Code, RuleID: key.Rule, RuleVersion: key.RuleVersion, GroupID: key.Group, PathID: key.Path, OutcomeCode: key.Outcome}}
			}
			bucket := buckets[key]
			bucket.Completion = append(bucket.Completion, instance.Bottlenecks.StateSeconds)
			bucket.Blocked = append(bucket.Blocked, instance.Bottlenecks.BlockedSeconds)
		}
		for _, observation := range observations {
			h := observation.History
			key := workflowDecisionOutcomeKey{Version: effectiveWorkflowContractVersion(h.ContractVersion), Outcome: instance.Current.OutcomeCode}
			if reason := h.ChangeReason; reason != nil {
				decision := key
				decision.Kind = "decision"
				decision.Code = reason.Decision.Code
				decision.Rule = reason.Decision.RuleID
				decision.RuleVersion = reason.Decision.RuleVersion
				add(decision)
			}
			for _, p := range h.SelectedPaths {
				path := key
				path.Kind = "path"
				path.Group = p.GroupID
				path.Path = p.PathID
				path.Code = p.DecisionCode
				path.Rule = p.DecisionRuleID
				path.RuleVersion = p.DecisionRuleVersion
				add(path)
				if p.DecisionMilestoneID != "" {
					decision := path
					decision.Kind = "decision"
					decision.Group = ""
					decision.Path = ""
					add(decision)
				}
			}
			// A reason reported after completion does not classify the completed business path.
			if observation.Terminal {
				break
			}
		}
		if len(seen) == 0 {
			out.UnclassifiedWorkflowCount++
		} else {
			out.LinkedWorkflowCount++
		}
	}
	keys := make([]workflowDecisionOutcomeKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	for _, key := range keys {
		bucket := buckets[key]
		bucket.Group.CompletionTime = workflowDurationDistribution(bucket.Completion)
		bucket.Group.BlockedTime = workflowDurationDistribution(bucket.Blocked)
		out.Groups = append(out.Groups, bucket.Group)
	}
	return out
}

func decisionOutcomeKeyLess(a, b workflowDecisionOutcomeKey) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	left := [...]string{a.Kind, a.Code, a.Rule, a.RuleVersion, a.Group, a.Path, a.Outcome}
	right := [...]string{b.Kind, b.Code, b.Rule, b.RuleVersion, b.Group, b.Path, b.Outcome}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}
