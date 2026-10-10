package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func blockerFollowUpMatches(b api.OperationWorkflowBlocker, reason string, at time.Time) bool {
	switch reason {
	case "unacknowledged":
		return b.AcknowledgedAt == ""
	case "follow_up_overdue":
		due, err := time.Parse(time.RFC3339Nano, b.FollowUpAt)
		return err == nil && !at.Before(due)
	default:
		return true
	}
}
func workflowFollowUpMatches(blockers []api.OperationWorkflowBlocker, opts api.OperationWorkflowAttentionOptions, at time.Time) bool {
	if opts.Reason != "unacknowledged" && opts.Reason != "follow_up_overdue" {
		return true
	}
	for _, b := range blockers {
		if operationAttentionBlockerMatches(b, opts) && blockerFollowUpMatches(b, opts.Reason, at) {
			return true
		}
	}
	return false
}
func workflowFollowUpReasons(state api.OperationWorkflowState, at time.Time) []string {
	if state.Terminal {
		return nil
	}
	var result []string
	for _, reason := range []string{"unacknowledged", "follow_up_overdue"} {
		if workflowFollowUpMatches(state.Blockers, api.OperationWorkflowAttentionOptions{Reason: reason}, at) {
			result = append(result, reason)
		}
	}
	return result
}
func addWorkflowFollowUpStats(stats *api.OperationWorkflowAttentionStats, blockers []api.OperationWorkflowBlocker, at time.Time) {
	for _, b := range blockers {
		if blockerFollowUpMatches(b, "unacknowledged", at) {
			stats.UnacknowledgedBlockerCount++
		}
		if blockerFollowUpMatches(b, "follow_up_overdue", at) {
			stats.FollowUpOverdueBlockerCount++
		}
	}
}
