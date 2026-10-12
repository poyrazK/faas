package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowOverdueConditionEvidence(instance workflowPerformanceInstance, at time.Time) map[workflowConditionOutcomeKey]api.OperationWorkflowDecisionOutcomeEvidence {
	out := map[workflowConditionOutcomeKey]api.OperationWorkflowDecisionOutcomeEvidence{}
	if instance.Current.Terminal || instance.Bottlenecks == nil || !instance.Bottlenecks.HistoryComplete {
		return out
	}
	facts, status := workflowActiveConditionEvidence(instance)
	if status != "" {
		return out
	}
	for key, fact := range facts {
		workflowEvaluateConditionFreshness(&fact, at)
		if fact.ConditionFreshness.Status != "overdue" {
			continue
		}
		age := workflowConditionEvidenceAge(fact, at)
		fact.EvidenceAgeSeconds = &age
		out[key] = fact
	}
	return out
}

type workflowConditionRefreshOwnerBucket struct {
	Group            api.OperationWorkflowConditionRefreshOwnerGroup
	Elapsed, Blocked []int64
}

func workflowConditionRefreshOwners(instances []workflowPerformanceInstance, at time.Time, summary *api.OperationWorkflowActiveConditionSummary) {
	summary.RefreshOwners = []api.OperationWorkflowConditionRefreshOwnerGroup{}
	buckets := map[string]*workflowConditionRefreshOwnerBucket{}
	for _, instance := range instances {
		facts := workflowOverdueConditionEvidence(instance, at)
		if len(facts) == 0 {
			continue
		}
		summary.OverdueWorkflowCount++
		summary.OverdueConditionCount += int64(len(facts))
		owners := map[string]int64{}
		for _, fact := range facts {
			owners[fact.ConditionFreshness.RefreshOwner]++
		}
		for owner, count := range owners {
			bucket := buckets[owner]
			if bucket == nil {
				if len(buckets) == api.OperationWorkflowConditionRefreshOwnersMax {
					summary.RefreshOwnersTruncated = true
					largest := ""
					for existing := range buckets {
						if existing > largest {
							largest = existing
						}
					}
					// The display threshold only decreases: discarded owners cannot later re-enter.
					if owner >= largest {
						continue
					}
					delete(buckets, largest)
				}
				bucket = &workflowConditionRefreshOwnerBucket{Group: api.OperationWorkflowConditionRefreshOwnerGroup{Owner: owner, Unassigned: owner == ""}}
				buckets[owner] = bucket
			}
			bucket.Group.OverdueConditionCount += count
			bucket.Elapsed = append(bucket.Elapsed, instance.Bottlenecks.StateSeconds)
			bucket.Blocked = append(bucket.Blocked, instance.Bottlenecks.BlockedSeconds)
		}
	}
	owners := make([]string, 0, len(buckets))
	for owner := range buckets {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		bucket := buckets[owner]
		bucket.Group.ElapsedTime = workflowDurationDistribution(bucket.Elapsed)
		bucket.Group.BlockedTime = workflowDurationDistribution(bucket.Blocked)
		summary.RefreshOwners = append(summary.RefreshOwners, bucket.Group)
	}
}
func workflowConditionRefreshContribution(instance workflowPerformanceInstance, owner string, at time.Time) ([]api.OperationWorkflowDecisionOutcomeEvidence, int64) {
	facts := workflowOverdueConditionEvidence(instance, at)
	keys := make([]workflowConditionOutcomeKey, 0, len(facts))
	for key, fact := range facts {
		if fact.ConditionFreshness.RefreshOwner == owner {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return workflowConditionOutcomeKeyLess(keys[i], keys[j]) })
	count := int64(len(keys))
	if len(keys) > api.OperationWorkflowConditionRefreshEvidenceMax {
		keys = keys[:api.OperationWorkflowConditionRefreshEvidenceMax]
	}
	out := make([]api.OperationWorkflowDecisionOutcomeEvidence, 0, len(keys))
	for _, key := range keys {
		out = append(out, facts[key])
	}
	return out, count
}
