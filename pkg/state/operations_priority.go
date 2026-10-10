package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func effectiveBlockerPriority(b api.OperationWorkflowBlocker) string {
	if b.Priority == "" {
		return "normal"
	}
	return b.Priority
}
func validAttentionPriority(priority string) bool {
	switch priority {
	case "", "low", "normal", "high", "urgent":
		return true
	}
	return false
}
func addWorkflowPriorityStats(stats *api.OperationWorkflowAttentionStats, blockers []api.OperationWorkflowBlocker) {
	for _, b := range blockers {
		switch effectiveBlockerPriority(b) {
		case "low":
			stats.LowBlockerCount++
		case "normal":
			stats.NormalBlockerCount++
		case "high":
			stats.HighBlockerCount++
		case "urgent":
			stats.UrgentBlockerCount++
		}
	}
}
func compareAttentionDeadline(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	left, _ := time.Parse(time.RFC3339Nano, a)
	right, _ := time.Parse(time.RFC3339Nano, b)
	if left.Before(right) {
		return -1
	}
	if left.After(right) {
		return 1
	}
	return 0
}
func attentionAfterCursor(state api.OperationWorkflowState, key string, cursor operationAttentionCursor) bool {
	if cursor.Key == "" {
		return true
	}
	if cursor.Sort == "deadline" {
		if compared := compareAttentionDeadline(state.DeadlineAt, cursor.DeadlineAt); compared != 0 {
			return compared > 0
		}
	}
	return state.UpdatedAt.Before(cursor.UpdatedAt) || state.UpdatedAt.Equal(cursor.UpdatedAt) && key < cursor.Key
}
