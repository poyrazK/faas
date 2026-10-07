package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WorkflowScheduleOccurrence records an evaluated due minute without customer
// input, output, or credentials. Run identity survives run retention.
type WorkflowScheduleOccurrence struct {
	ID               string    `json:"id"`
	AppID            string    `json:"app_id"`
	PlatformTenantID string    `json:"platform_tenant_id,omitempty"`
	WorkflowName     string    `json:"workflow_name"`
	DeploymentID     string    `json:"deployment_id"`
	ScheduledFor     time.Time `json:"scheduled_for"`
	EvaluatedAt      time.Time `json:"evaluated_at"`
	Status           string    `json:"status"`
	RunID            string    `json:"run_id,omitempty"`
}

type WorkflowScheduleHistoryStore interface {
	ListWorkflowScheduleOccurrences(context.Context, string, string, string, int) ([]WorkflowScheduleOccurrence, error)
	PruneWorkflowScheduleOccurrences(context.Context, time.Time, int) (int, error)
}

func workflowScheduleOccurrence(cursor *WorkflowScheduleCursor, now time.Time) *WorkflowScheduleOccurrence {
	if cursor.ScheduledFor == nil || !cursor.ScheduledFor.Equal(now.UTC().Truncate(time.Minute)) || cursor.Status == WorkflowScheduleArmed {
		return nil
	}
	return &WorkflowScheduleOccurrence{ID: uuid.NewString(), AppID: cursor.AppID,
		PlatformTenantID: cursor.PlatformTenantID, WorkflowName: cursor.WorkflowName,
		DeploymentID: cursor.DeploymentID, ScheduledFor: *cursor.ScheduledFor,
		EvaluatedAt: cursor.LastEvaluatedAt, Status: cursor.Status, RunID: cursor.LastRunID}
}
