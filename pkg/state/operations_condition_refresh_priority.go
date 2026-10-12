package state

import (
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func conditionRefreshPriorityDimension(d string) bool {
	return d == "condition_refresh_age" || d == "condition_refresh_blocked"
}
func workflowConditionRefreshPriorityContribution(instance workflowPerformanceInstance, dimension string, at time.Time) (int64, *api.OperationWorkflowConditionRefreshPriority, []api.OperationWorkflowDecisionOutcomeEvidence, int64) {
	facts := workflowOverdueConditionEvidence(instance, at)
	if len(facts) == 0 {
		return 0, nil, nil, 0
	}
	keys := make([]workflowConditionOutcomeKey, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return workflowConditionOutcomeKeyLess(keys[i], keys[j]) })
	priority := &api.OperationWorkflowConditionRefreshPriority{OldestEvidence: facts[keys[0]], BlockedSeconds: instance.Bottlenecks.BlockedSeconds, RefreshOwners: []string{}}
	owners := map[string]bool{}
	for _, key := range keys {
		fact := facts[key]
		if fact.OccurredAt.Before(priority.OldestEvidence.OccurredAt) {
			priority.OldestEvidence = fact
		}
		if fact.ConditionFreshness.RefreshOwner == "" {
			priority.UnassignedConditionCount++
		} else {
			owners[fact.ConditionFreshness.RefreshOwner] = true
		}
	}
	for owner := range owners {
		priority.RefreshOwners = append(priority.RefreshOwners, owner)
	}
	sort.Strings(priority.RefreshOwners)
	priority.OldestEvidenceAgeSeconds = workflowConditionEvidenceAge(priority.OldestEvidence, at)
	count := int64(len(keys))
	if len(keys) > api.OperationWorkflowConditionRefreshEvidenceMax {
		keys = keys[:api.OperationWorkflowConditionRefreshEvidenceMax]
	}
	preview := make([]api.OperationWorkflowDecisionOutcomeEvidence, 0, len(keys))
	for _, key := range keys {
		preview = append(preview, facts[key])
	}
	seconds := priority.OldestEvidenceAgeSeconds
	priority.Explanation = fmt.Sprintf("Ranked by the oldest explicitly reported overdue condition: report %s is %d seconds old at evaluation. This measures report age, not time past its deadline or business severity.", priority.OldestEvidence.ReportID, seconds)
	if dimension == "condition_refresh_blocked" {
		seconds = priority.BlockedSeconds
		priority.Explanation = fmt.Sprintf("Ranked by %d seconds of blocked duration across the full retained workflow. It has %d overdue condition reports; the duration is not attributed to those conditions or their refresh owners.", seconds, count)
	}
	return seconds, priority, preview, count
}
