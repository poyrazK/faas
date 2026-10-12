package state

import (
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func conditionGapProgressDimension(d string) bool {
	return d == "condition_gap_owner" || d == "condition_gap_queue"
}
func workflowGapQueueContribution(instance workflowPerformanceInstance, definitions map[string][]api.OperationWorkflowSpec, baselineAt, at time.Time, filter api.OperationWorkflowPerformanceGroup) (*api.OperationWorkflowNewMissingConditionProgressInstance, *api.OperationWorkflowGapQueuePriority) {
	item, status := workflowNewMissingProgressInstance(instance, definitions, baselineAt, at)
	if status != "" {
		return nil, nil
	}
	priority := &api.OperationWorkflowGapQueuePriority{MissingConditionCount: item.CurrentMissingConditionCount, Statuses: item.Statuses, RefreshOwners: []string{}, Guidance: []api.OperationWorkflowGapQueueGuidance{}, BlockedSeconds: workflowGapProgressBlockedSeconds(instance, at)}
	owners := map[string]bool{}
	counts := map[string]int64{}
	preview := []api.OperationWorkflowNewMissingConditionProgressChange{}
	var selected int64
	for _, change := range item.Changes {
		owner := change.After.Definition.RefreshOwner
		if owner == "" {
			priority.UnassignedConditionCount++
		} else {
			owners[owner] = true
		}
		priority.Guidance = append(priority.Guidance, api.OperationWorkflowGapQueueGuidance{Status: change.Status, Expectation: change.After})
		if filter.GapStatus != "" && change.Status != filter.GapStatus || (filter.Owner != "" || filter.Unassigned) && owner != filter.Owner {
			continue
		}
		selected++
		counts[change.Status]++
		if len(preview) < api.OperationWorkflowConditionRefreshEvidenceMax {
			preview = append(preview, change)
		}
	}
	if selected == 0 {
		return nil, nil
	}
	for owner := range owners {
		priority.RefreshOwners = append(priority.RefreshOwners, owner)
	}
	sort.Strings(priority.RefreshOwners)
	priority.Explanation = fmt.Sprintf("Ranked by %d seconds of observed report-only blocked intervals. This workflow has %d current reporting gaps across all owners and categories; %d match the selected filters. Intervals spanning contract boundaries are omitted. This is not gap age, causation or business severity.", priority.BlockedSeconds, priority.MissingConditionCount, selected)
	item.CurrentMissingConditionCount = selected
	item.Statuses = workflowNewMissingProgressCounts(counts)
	item.Changes = preview
	item.ChangesTruncated = selected > int64(len(preview))
	return &item, priority
}
