package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// WorkflowScheduleOccurrence records an evaluated due minute without customer
// input, output, or credentials. Run identity survives run retention.
type WorkflowScheduleOccurrence struct {
	ID               string     `json:"id"`
	AppID            string     `json:"app_id"`
	PlatformTenantID string     `json:"platform_tenant_id,omitempty"`
	WorkflowName     string     `json:"workflow_name"`
	DeploymentID     string     `json:"deployment_id"`
	ScheduledFor     time.Time  `json:"scheduled_for"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	Status           string     `json:"status"`
	RunID            string     `json:"run_id,omitempty"`
	DefinitionHash   string     `json:"-"`
	ReplayRunID      string     `json:"replay_run_id,omitempty"`
	ReplayedAt       *time.Time `json:"replayed_at,omitempty"`
}

type WorkflowScheduleHistoryStore interface {
	ListWorkflowScheduleOccurrences(context.Context, string, string, string, int) ([]WorkflowScheduleOccurrence, error)
	PruneWorkflowScheduleOccurrences(context.Context, time.Time, int) (int, error)
}

func workflowScheduleDefinitionHash(definition api.WorkflowSpec) string {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func workflowScheduleOccurrence(cursor, previous *WorkflowScheduleCursor, definition api.WorkflowSpec) *WorkflowScheduleOccurrence {
	if cursor.ScheduledFor == nil || cursor.Status == WorkflowScheduleArmed ||
		previous != nil && previous.ScheduledFor != nil && cursor.ScheduledFor.Equal(*previous.ScheduledFor) {
		return nil
	}
	return &WorkflowScheduleOccurrence{ID: uuid.NewString(), AppID: cursor.AppID,
		PlatformTenantID: cursor.PlatformTenantID, WorkflowName: cursor.WorkflowName,
		DeploymentID: cursor.DeploymentID, ScheduledFor: *cursor.ScheduledFor,
		EvaluatedAt: cursor.LastEvaluatedAt, Status: cursor.Status, RunID: cursor.LastRunID,
		DefinitionHash: workflowScheduleDefinitionHash(definition)}
}
