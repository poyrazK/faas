package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessReminderWorkerStore interface {
	PublishDueWorkflowReadinessReminders(context.Context) (int, error)
}

func readinessReminderDue(n api.OperationWorkflowReadinessNotification, now time.Time) bool {
	return n.Coordination != nil && n.Coordination.FollowUpAt != nil && !n.Coordination.FollowUpAt.After(now) && (n.Reminder == nil || !n.Reminder.DueAt.Equal(*n.Coordination.FollowUpAt))
}
func readinessDueReminder(n api.OperationWorkflowReadinessNotification, now time.Time) *api.OperationWorkflowReadinessDueReminder {
	due := n.Coordination.FollowUpAt.UTC()
	// Stable across retries, replicas, restarts, and scheduling the same deadline again.
	key := n.ID + "\x1f" + due.Format(time.RFC3339Nano)
	return &api.OperationWorkflowReadinessDueReminder{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String(), DueAt: due, CreatedAt: now.UTC().Truncate(time.Microsecond)}
}
