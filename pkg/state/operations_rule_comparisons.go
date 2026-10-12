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
	MultiVersion, EligibleMultiVersion int64
	Examples                           []api.OperationWorkflowRuleVersionOverlap
	ExamplesTruncated                  bool
	Versions                           map[string]*workflowRuleVersionBucket
	Truncated                          bool
}
type workflowRuleVersionBucket struct {
	Single              *workflowRuleVersionBucket
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
	instances = append([]workflowPerformanceInstance(nil), instances...)
	sort.Slice(instances, func(i, j int) bool {
		a, b := instances[i], instances[j]
		return operationAttentionKey(a.Tenant, a.Subject, a.Current.Workflow, a.Current.InstanceID) < operationAttentionKey(b.Tenant, b.Subject, b.Current.Workflow, b.Current.InstanceID)
	})
	for _, instance := range instances {

		seen := map[workflowRuleVersionKey]api.OperationWorkflowDecisionOutcomeEvidence{}
		counts := map[workflowRuleKey]int{}
		visitWorkflowDecisionOutcomeReports(instance, func(key workflowDecisionOutcomeKey, fact api.OperationWorkflowDecisionOutcomeEvidence) bool {
			if key.Kind != "decision" {
				return true
			}
			identity := workflowRuleVersionKey{Rule: workflowRuleKey{Version: key.Version, Code: key.Code, Rule: key.Rule}, Version: key.RuleVersion}
			if _, exists := seen[identity]; !exists {
				seen[identity] = fact
				counts[identity.Rule]++
			}
			return true
		})
		identities := make([]workflowRuleVersionKey, 0, len(seen))
		for key := range seen {
			identities = append(identities, key)
		}
		sort.Slice(identities, func(i, j int) bool {
			a, b := identities[i], identities[j]
			if a.Rule != b.Rule {
				return ruleKeyLess(a.Rule, b.Rule)
			}
			return a.Version < b.Version
		})
		updated := map[workflowRuleKey]bool{}
		for _, identity := range identities {
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
						continue
					}
					delete(rules, largest)
				}
				bucket = &workflowRuleBucket{Versions: map[string]*workflowRuleVersionBucket{}}
				rules[identity.Rule] = bucket
			}

			if !updated[identity.Rule] {
				updated[identity.Rule] = true
				if counts[identity.Rule] > 1 {
					bucket.MultiVersion++
					complete := instance.Bottlenecks != nil && instance.Bottlenecks.HistoryComplete
					eligible := complete && instance.Current.OutcomeCode != ""
					if eligible {
						bucket.EligibleMultiVersion++
					}
					if len(bucket.Examples) < api.OperationWorkflowRuleOverlapExamplesMax {
						example := api.OperationWorkflowRuleVersionOverlap{PlatformTenantID: instance.Tenant, Subject: instance.Subject, InstanceID: instance.Current.InstanceID, OperationID: instance.Current.OperationID, HistoryComplete: complete, Eligible: eligible, Evidence: []api.OperationWorkflowDecisionOutcomeEvidence{}}
						facts := []api.OperationWorkflowDecisionOutcomeEvidence{}
						for _, candidate := range identities {
							if candidate.Rule == identity.Rule {
								facts = append(facts, seen[candidate])
							}
						}
						sort.Slice(facts, func(i, j int) bool {
							if facts[i].Revision != facts[j].Revision {
								return facts[i].Revision < facts[j].Revision
							}
							if facts[i].OperationID != facts[j].OperationID {
								return facts[i].OperationID < facts[j].OperationID
							}
							if facts[i].ReportID != facts[j].ReportID {
								return facts[i].ReportID < facts[j].ReportID
							}
							return facts[i].Decision.RuleVersion < facts[j].Decision.RuleVersion
						})
						if len(facts) > api.OperationWorkflowRuleOverlapEvidenceMax {
							facts = facts[:api.OperationWorkflowRuleOverlapEvidenceMax]
							example.EvidenceTruncated = true
						}
						example.Evidence = facts
						bucket.Examples = append(bucket.Examples, example)
					} else {
						bucket.ExamplesTruncated = true
					}
				}
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
						continue
					}
					delete(bucket.Versions, largest)
				}
				version = &workflowRuleVersionBucket{Statistics: api.OperationWorkflowRuleVersionStatistics{RuleVersion: identity.Version, Outcomes: []api.OperationWorkflowRuleVersionOutcome{}}, Outcomes: map[string]int64{}}
				bucket.Versions[identity.Version] = version
			}
			version.Statistics.ObservedWorkflowCount++
			if instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
				version.Statistics.ExcludedIncompleteWorkflowCount++
				continue
			}
			if instance.Current.OutcomeCode == "" {
				version.Statistics.MissingOutcomeWorkflowCount++
				continue
			}

			accumulateRuleVersionOutcome(version, instance)
			if counts[identity.Rule] == 1 {
				if version.Single == nil {
					version.Single = &workflowRuleVersionBucket{Outcomes: map[string]int64{}}
				}
				accumulateRuleVersionOutcome(version.Single, instance)
			}
		}

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
		comparison := api.OperationWorkflowRuleComparison{SingleVersionStatus: "insufficient_data", MultiVersionWorkflowCount: bucket.MultiVersion, EligibleMultiVersionWorkflowCount: bucket.EligibleMultiVersion, MultiVersionExamples: bucket.Examples, ExamplesTruncated: bucket.ExamplesTruncated, ContractVersion: key.Version, DecisionCode: key.Code, RuleID: key.Rule, Status: "insufficient_data", VersionsTruncated: bucket.Truncated, Versions: []api.OperationWorkflowRuleVersionStatistics{}}
		versions := make([]string, 0, len(bucket.Versions))
		for version := range bucket.Versions {
			versions = append(versions, version)
		}
		sort.Strings(versions)
		if comparison.MultiVersionExamples == nil {
			comparison.MultiVersionExamples = []api.OperationWorkflowRuleVersionOverlap{}
		}
		eligibleVersions := 0
		singleVersions := 0
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

			stats.SingleVersion = &api.OperationWorkflowRuleVersionCohort{Outcomes: []api.OperationWorkflowRuleVersionOutcome{}}
			if single := version.Single; single != nil {
				singleVersions++
				stats.SingleVersion.EligibleWorkflowCount = single.Statistics.EligibleWorkflowCount
				stats.SingleVersion.CompletionTime = workflowDurationDistribution(single.Completion)
				stats.SingleVersion.BlockedTime = workflowDurationDistribution(single.Blocked)
				stats.SingleVersion.OutcomesTruncated = single.Statistics.OutcomesTruncated
				codes := make([]string, 0, len(single.Outcomes))
				for code := range single.Outcomes {
					codes = append(codes, code)
				}
				sort.Strings(codes)
				for _, code := range codes {
					count := single.Outcomes[code]
					stats.SingleVersion.Outcomes = append(stats.SingleVersion.Outcomes, api.OperationWorkflowRuleVersionOutcome{OutcomeCode: code, WorkflowCount: count, ProportionBasisPoints: count * 10000 / single.Statistics.EligibleWorkflowCount})
				}
			}
			comparison.Versions = append(comparison.Versions, stats)
		}
		if eligibleVersions >= 2 {
			comparison.Status = "compared"
		}
		if singleVersions >= 2 {
			comparison.SingleVersionStatus = "compared"
		}
		out.Comparisons = append(out.Comparisons, comparison)
	}
	return out
}

func accumulateRuleVersionOutcome(version *workflowRuleVersionBucket, instance workflowPerformanceInstance) {
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
			return
		}
		delete(version.Outcomes, largest)
	}
	version.Outcomes[outcome]++
}

func hideRuleOverlapTenants(out *api.OperationWorkflowPerformanceSummary) {
	if out.DecisionOutcomes != nil {
		hideRuleComparisonTenants(out.DecisionOutcomes.RuleComparisons)
	}
}
func hideRuleComparisonTenants(out *api.OperationWorkflowRuleComparisonSummary) {
	if out == nil {
		return
	}
	for i := range out.Comparisons {
		for j := range out.Comparisons[i].MultiVersionExamples {
			out.Comparisons[i].MultiVersionExamples[j].PlatformTenantID = ""
		}
	}
}
