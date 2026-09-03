package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Workflow run status constants (ADR-081 §1).
const (
	WorkflowRunStatusPending       = "pending"
	WorkflowRunStatusRunning       = "running"
	WorkflowRunStatusAwaitingEvent = "awaiting_event"
	WorkflowRunStatusSucceeded     = "succeeded"
	WorkflowRunStatusFailed        = "failed"
	WorkflowRunStatusDead          = "dead"
)

// Workflow step status constants (ADR-081 §1).
const (
	WorkflowStepStatusPending       = "pending"
	WorkflowStepStatusRunning       = "running"
	WorkflowStepStatusAwaitingEvent = "awaiting_event"
	WorkflowStepStatusSucceeded     = "succeeded"
	WorkflowStepStatusFailed        = "failed"
	WorkflowStepStatusDead          = "dead"
	WorkflowStepStatusSkipped       = "skipped"
)

var (
	ErrWorkflowRunNotFound   = errors.New("state: workflow run not found")
	ErrWorkflowStepNotFound  = errors.New("state: workflow step not found")
	ErrWorkflowEventNotFound = errors.New("state: workflow event not found")
	ErrWorkflowNotRunning    = errors.New("state: workflow run is not in running state")
)

// WorkflowRun is one row of public.workflow_runs.
type WorkflowRun struct {
	ID                 string          `json:"id"`
	AppID              string          `json:"app_id"`
	WorkflowName       string          `json:"workflow_name"`
	Status             string          `json:"status"`
	CurrentStep        *string         `json:"current_step,omitempty"`
	Input              json.RawMessage `json:"input"`
	Output             json.RawMessage `json:"output,omitempty"`
	DefinitionSnapshot json.RawMessage `json:"definition_snapshot"`
	ScheduledFor       time.Time       `json:"scheduled_for"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	LastError          *string         `json:"last_error,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// WorkflowStep is one row of public.workflow_steps.
type WorkflowStep struct {
	RunID      string          `json:"run_id"`
	StepName   string          `json:"step_name"`
	Status     string          `json:"status"`
	Attempt    int             `json:"attempt"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// WorkflowEvent is one row of public.workflow_events.
type WorkflowEvent struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	EventName  string          `json:"event_name"`
	Payload    json.RawMessage `json:"payload"`
	ReceivedAt time.Time       `json:"received_at"`
}

// ListWorkflowRunsOpts controls pagination and filtering for workflow runs.
type ListWorkflowRunsOpts struct {
	Status string
	Limit  int
	Offset int
}

// WorkflowStore defines the storage operations for durable workflows.
type WorkflowStore interface {
	// Runs
	CreateWorkflowRun(ctx context.Context, r *WorkflowRun) error
	GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error)
	ListWorkflowRuns(ctx context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error)
	MarkWorkflowRunStatus(ctx context.Context, id, status string, output json.RawMessage, lastErr *string) error
	ClaimNextPendingRun(ctx context.Context) (*WorkflowRun, error)
	CountActiveRunsByApp(ctx context.Context, appID string) (int, error)

	// Steps
	CreateWorkflowSteps(ctx context.Context, runID string, steps []*WorkflowStep) error
	GetWorkflowSteps(ctx context.Context, runID string) ([]*WorkflowStep, error)
	MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error

	// Events
	InsertWorkflowEvent(ctx context.Context, e *WorkflowEvent) error
	GetWorkflowEventsForRun(ctx context.Context, runID string) ([]*WorkflowEvent, error)
	FindMatchingEvent(ctx context.Context, runID, eventName string) (*WorkflowEvent, error)

	// Retention
	SweepExpiredWorkflowRuns(ctx context.Context, olderThan time.Duration) (int, error)
	SweepExpiredWorkflowEvents(ctx context.Context, olderThan time.Duration) (int, error)
}
