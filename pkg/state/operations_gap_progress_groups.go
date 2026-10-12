package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowProgressDefinitionReader interface {
	workflowProgressDefinitions(context.Context, string, string, api.OperationWorkflowPerformanceOptions, bool, []string) (map[string][]api.OperationWorkflowSpec, error)
}
type workflowGapProgressGroupKey struct {
	Version              int
	State, Owner, Status string
}

func validWorkflowGapStatus(status string) bool {
	switch status {
	case "report_lost", "new_expectation", "still_missing", "expectation_changed", "contract_changed":
		return true
	}
	return false
}

// Report comparisons permit contract boundaries; rank by reported blocked intervals,
// without relaxing the eligibility of the existing duration groups.
func workflowGapProgressBlockedSeconds(instance workflowPerformanceInstance, at time.Time) int64 {
	return calculateWorkflowBottlenecks(instance.Observations, &instance.Current, nil, at, false).BlockedSeconds
}
func workflowGapProgressBuckets(instance workflowPerformanceInstance, item api.OperationWorkflowNewMissingConditionProgressInstance, buckets map[workflowGapProgressGroupKey]*workflowMissingConditionBucket, at time.Time) {
	counts := map[workflowGapProgressGroupKey]int64{}
	for _, change := range item.Changes {
		key := workflowGapProgressGroupKey{effectiveWorkflowContractVersion(instance.Current.ContractVersion), instance.Current.State, change.After.Definition.RefreshOwner, change.Status}
		counts[key]++
	}
	seconds := workflowGapProgressBlockedSeconds(instance, at)
	for key, count := range counts {
		bucket := buckets[key]
		if bucket == nil {
			bucket = &workflowMissingConditionBucket{}
			buckets[key] = bucket
		}
		bucket.Count += count
		bucket.Blocked = append(bucket.Blocked, seconds)
	}
}
func workflowGapProgressGroups(out *api.OperationWorkflowNewMissingConditionProgress, buckets map[workflowGapProgressGroupKey]*workflowMissingConditionBucket) {
	keys := make([]workflowGapProgressGroupKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.State != b.State {
			return a.State < b.State
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Status < b.Status
	})
	out.Groups = []api.OperationWorkflowGapProgressGroup{}
	if len(keys) > api.OperationWorkflowActiveConditionGroupsMax {
		keys = keys[:api.OperationWorkflowActiveConditionGroupsMax]
		out.GroupsTruncated = true
	}
	for _, key := range keys {
		bucket := buckets[key]
		out.Groups = append(out.Groups, api.OperationWorkflowGapProgressGroup{ContractVersion: key.Version, State: key.State, Owner: key.Owner, Unassigned: key.Owner == "", GapStatus: key.Status, MissingConditionCount: bucket.Count, BlockedTime: workflowDurationDistribution(bucket.Blocked)})
	}
}
func workflowGapProgressGroupMatches(group api.OperationWorkflowGapProgressGroup, selector api.OperationWorkflowPerformanceGroup) bool {
	return group.ContractVersion == selector.ContractVersion && group.State == selector.State && group.Owner == selector.Owner && group.Unassigned == selector.Unassigned && group.GapStatus == selector.GapStatus
}
func workflowGapProgressContribution(instance workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time, group api.OperationWorkflowPerformanceGroup) *api.OperationWorkflowNewMissingConditionProgressInstance {
	if effectiveWorkflowContractVersion(instance.Current.ContractVersion) != group.ContractVersion || instance.Current.State != group.State {
		return nil
	}
	item, status := workflowNewMissingProgressInstance(instance, definitions, baselineAt, at)
	if status != "" {
		return nil
	}
	filtered := []api.OperationWorkflowNewMissingConditionProgressChange{}
	var count int64
	for _, change := range item.Changes {
		if change.Status != group.GapStatus || change.After.Definition.RefreshOwner != group.Owner {
			continue
		}
		count++
		if len(filtered) < api.OperationWorkflowConditionRefreshEvidenceMax {
			filtered = append(filtered, change)
		}
	}
	if count == 0 {
		return nil
	}
	item.Changes = filtered
	item.ChangesTruncated = count > int64(len(filtered))
	item.CurrentMissingConditionCount = count
	item.Statuses = workflowNewMissingProgressCounts(map[string]int64{group.GapStatus: count})
	return &item
}
