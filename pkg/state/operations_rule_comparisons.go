package state

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowRuleKey struct {
	Version    int
	Code, Rule string
}
type workflowRuleVersionKey struct {
	Rule    workflowRuleKey
	Version string
}
type workflowRuleBucket struct {
	Versions  map[string]*workflowRuleVersionBucket
	Truncated bool
}
type workflowRuleVersionBucket struct {
	Statistics          api.OperationWorkflowRuleVersionStatistics
	Completion, Blocked []int64
	Outcomes            map[string]int64
}

func ruleKeyLess(a, b workflowRuleKey) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	return a.Rule < b.Rule
}

func workflowRuleComparisons(instances []workflowPerformanceInstance) *api.OperationWorkflowRuleComparisonSummary {
	out := &api.OperationWorkflowRuleComparisonSummary{SampledWorkflowCount: int64(len(instances)), Comparisons: []api.OperationWorkflowRuleComparison{}}
	rules := map[workflowRuleKey]*workflowRuleBucket{}
	for _, instance := range instances {
		seen := map[workflowRuleVersionKey]bool{}
		visitWorkflowDecisionOutcomeReports(instance, func(key workflowDecisionOutcomeKey, _ api.OperationWorkflowDecisionOutcomeEvidence) bool {
			if key.Kind != "decision" {
				return true
			}
			identity := workflowRuleVersionKey{Rule: workflowRuleKey{Version: key.Version, Code: key.Code, Rule: key.Rule}, Version: key.RuleVersion}
			if seen[identity] {
				return true
			}
			seen[identity] = true
			bucket := rules[identity.Rule]
			if bucket == nil {
				if len(rules) == api.OperationWorkflowRuleComparisonsMax {
					out.ComparisonsTruncated = true
					var largest workflowRuleKey
					first := true
					for existing := range rules {
						if first || ruleKeyLess(largest, existing) {
							largest = existing
							first = false
						}
					}
					if !ruleKeyLess(identity.Rule, largest) {
						return true
					}
					delete(rules, largest)
				}
				bucket = &workflowRuleBucket{Versions: map[string]*workflowRuleVersionBucket{}}
				rules[identity.Rule] = bucket
			}
			version := bucket.Versions[identity.Version]
			if version == nil {
				if len(bucket.Versions) == api.OperationWorkflowRuleVersionsMax {
					bucket.Truncated = true
					largest := ""
					for existing := range bucket.Versions {
						if existing > largest {
							largest = existing
						}
					}
					if identity.Version >= largest {
						return true
					}
					delete(bucket.Versions, largest)
				}
				version = &workflowRuleVersionBucket{Statistics: api.OperationWorkflowRuleVersionStatistics{RuleVersion: identity.Version, Outcomes: []api.OperationWorkflowRuleVersionOutcome{}}, Outcomes: map[string]int64{}}
				bucket.Versions[identity.Version] = version
			}
			version.Statistics.ObservedWorkflowCount++
			if instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
				version.Statistics.ExcludedIncompleteWorkflowCount++
				return true
			}
			if instance.Current.OutcomeCode == "" {
				version.Statistics.MissingOutcomeWorkflowCount++
				return true
			}
			version.Statistics.EligibleWorkflowCount++
			version.Completion = append(version.Completion, instance.Bottlenecks.StateSeconds)
			version.Blocked = append(version.Blocked, instance.Bottlenecks.BlockedSeconds)
			outcome := instance.Current.OutcomeCode
			if _, exists := version.Outcomes[outcome]; !exists && len(version.Outcomes) == api.OperationWorkflowRuleOutcomesMax {
				version.Statistics.OutcomesTruncated = true
				largest := ""
				for existing := range version.Outcomes {
					if existing > largest {
						largest = existing
					}
				}
				if outcome >= largest {
					return true
				}
				delete(version.Outcomes, largest)
			}
			version.Outcomes[outcome]++
			return true
		})
		if len(seen) == 0 {
			out.UnattributedWorkflowCount++
		}
	}
	keys := make([]workflowRuleKey, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return ruleKeyLess(keys[i], keys[j]) })
	for _, key := range keys {
		bucket := rules[key]
		comparison := api.OperationWorkflowRuleComparison{ContractVersion: key.Version, DecisionCode: key.Code, RuleID: key.Rule, Status: "insufficient_data", VersionsTruncated: bucket.Truncated, Versions: []api.OperationWorkflowRuleVersionStatistics{}}
		versions := make([]string, 0, len(bucket.Versions))
		for version := range bucket.Versions {
			versions = append(versions, version)
		}
		sort.Strings(versions)
		eligibleVersions := 0
		for _, name := range versions {
			version := bucket.Versions[name]
			stats := version.Statistics
			stats.CompletionTime = workflowDurationDistribution(version.Completion)
			stats.BlockedTime = workflowDurationDistribution(version.Blocked)
			if stats.EligibleWorkflowCount > 0 {
				eligibleVersions++
			}
			outcomes := make([]string, 0, len(version.Outcomes))
			for outcome := range version.Outcomes {
				outcomes = append(outcomes, outcome)
			}
			sort.Strings(outcomes)
			for _, outcome := range outcomes {
				count := version.Outcomes[outcome]
				stats.Outcomes = append(stats.Outcomes, api.OperationWorkflowRuleVersionOutcome{OutcomeCode: outcome, WorkflowCount: count, ProportionBasisPoints: count * 10000 / stats.EligibleWorkflowCount})
			}
			comparison.Versions = append(comparison.Versions, stats)
		}
		if eligibleVersions >= 2 {
			comparison.Status = "compared"
		}
		out.Comparisons = append(out.Comparisons, comparison)
	}
	return out
}
