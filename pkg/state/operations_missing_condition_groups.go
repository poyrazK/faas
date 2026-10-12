package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowMissingConditionGroupKey struct {
	Version      int
	State, Owner string
}
type workflowMissingConditionBucket struct {
	Count   int64
	Blocked []int64
}

func workflowMissingConditionGroupLess(a, b workflowMissingConditionGroupKey) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	if a.State != b.State {
		return a.State < b.State
	}
	return a.Owner < b.Owner
}
func workflowMissingConditionBuckets(instance workflowPerformanceInstance, item api.OperationWorkflowConditionCoverageInstance, buckets map[workflowMissingConditionGroupKey]*workflowMissingConditionBucket) {
	counts := map[string]int64{}
	for _, condition := range item.Conditions {
		if condition.Status == "missing" {
			counts[condition.Definition.RefreshOwner]++
		}
	}
	for owner, count := range counts {
		key := workflowMissingConditionGroupKey{effectiveWorkflowContractVersion(instance.Current.ContractVersion), instance.Current.State, owner}
		bucket := buckets[key]
		if bucket == nil {
			bucket = &workflowMissingConditionBucket{}
			buckets[key] = bucket
		}
		bucket.Count += count
		bucket.Blocked = append(bucket.Blocked, instance.Bottlenecks.BlockedSeconds)
	}
}
func workflowMissingConditionGroups(out *api.OperationWorkflowConditionCoverage, buckets map[workflowMissingConditionGroupKey]*workflowMissingConditionBucket) {
	keys := make([]workflowMissingConditionGroupKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return workflowMissingConditionGroupLess(keys[i], keys[j]) })
	out.MissingGroups = []api.OperationWorkflowMissingConditionGroup{}
	if len(keys) > api.OperationWorkflowActiveConditionGroupsMax {
		keys = keys[:api.OperationWorkflowActiveConditionGroupsMax]
		out.MissingGroupsTruncated = true
	}
	for _, key := range keys {
		bucket := buckets[key]
		out.MissingGroups = append(out.MissingGroups, api.OperationWorkflowMissingConditionGroup{ContractVersion: key.Version, State: key.State, Owner: key.Owner, Unassigned: key.Owner == "", MissingConditionCount: bucket.Count, BlockedTime: workflowDurationDistribution(bucket.Blocked)})
	}
}
func workflowMissingConditionContribution(instance workflowPerformanceInstance, group api.OperationWorkflowPerformanceGroup, at time.Time) ([]api.OperationWorkflowConditionCoverageEntry, int64) {
	preview := []api.OperationWorkflowConditionCoverageEntry{}
	if effectiveWorkflowContractVersion(instance.Current.ContractVersion) != group.ContractVersion || instance.Current.State != group.State {
		return preview, 0
	}
	item, status := workflowConditionCoverageInstance(instance, at)
	if status != "" {
		return preview, 0
	}
	var count int64
	for _, condition := range item.Conditions {
		if condition.Status != "missing" || condition.Definition.RefreshOwner != group.Owner {
			continue
		}
		count++
		if len(preview) < api.OperationWorkflowConditionRefreshEvidenceMax {
			preview = append(preview, condition)
		}
	}
	return preview, count
}
