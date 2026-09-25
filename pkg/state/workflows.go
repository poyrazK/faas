package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
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
	ErrWorkflowRunNotFound       = errors.New("state: workflow run not found")
	ErrWorkflowStepNotFound      = errors.New("state: workflow step not found")
	ErrWorkflowEventNotFound     = errors.New("state: workflow event not found")
	ErrWorkflowNotRunning        = errors.New("state: workflow run is not in running state")
	ErrWorkflowInvalidStatus     = errors.New("state: invalid workflow status")
	ErrWorkflowInvalidAttempt    = errors.New("state: workflow attempt cannot be negative")
	ErrWorkflowInvalidPagination = errors.New("state: workflow pagination cannot be negative")
	ErrWorkflowInvalidInput      = errors.New("state: workflow JSON payload is invalid")
	ErrWorkflowInvalidRecord     = errors.New("state: workflow record is invalid")
	ErrWorkflowRunQuotaExceeded  = errors.New("state: workflow active-run quota exceeded")
	ErrWorkflowCallbackClosed    = errors.New("state: workflow callback is closed")
	ErrWorkflowCallbackExpired   = errors.New("state: workflow callback has expired")
)

// WorkflowRunStaleAfter is longer than the largest plan's two-hour step
// timeout. A schedd that disappears while owning a run therefore cannot cause
// concurrent execution, while another schedd will eventually recover it.
const WorkflowRunStaleAfter = 2*time.Hour + 5*time.Minute

func validateWorkflowRunStatus(status string) error {
	switch status {
	case WorkflowRunStatusPending, WorkflowRunStatusRunning,
		WorkflowRunStatusAwaitingEvent, WorkflowRunStatusSucceeded,
		WorkflowRunStatusFailed, WorkflowRunStatusDead:
		return nil
	default:
		return fmt.Errorf("%w: run status %q", ErrWorkflowInvalidStatus, status)
	}
}

func validateWorkflowStepStatus(status string) error {
	switch status {
	case WorkflowStepStatusPending, WorkflowStepStatusRunning,
		WorkflowStepStatusAwaitingEvent, WorkflowStepStatusSucceeded,
		WorkflowStepStatusFailed, WorkflowStepStatusDead, WorkflowStepStatusSkipped:
		return nil
	default:
		return fmt.Errorf("%w: step status %q", ErrWorkflowInvalidStatus, status)
	}
}

func validateWorkflowJSON(raw json.RawMessage, required bool) error {
	if len(raw) == 0 {
		if required {
			return fmt.Errorf("%w: required JSON payload is empty", ErrWorkflowInvalidInput)
		}
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%w: malformed JSON", ErrWorkflowInvalidInput)
	}
	return nil
}

func cloneWorkflowJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func equalWorkflowJSON(a, b json.RawMessage) bool {
	var left, right any
	leftDecoder := json.NewDecoder(bytes.NewReader(a))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(b))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&left) != nil || rightDecoder.Decode(&right) != nil {
		return false
	}
	return equalWorkflowValue(left, right)
}

func equalWorkflowValue(left, right any) bool {
	switch value := left.(type) {
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		a, validA := new(big.Rat).SetString(value.String())
		b, validB := new(big.Rat).SetString(other.String())
		return validA && validB && a.Cmp(b) == 0
	case []any:
		other, ok := right.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for i := range value {
			if !equalWorkflowValue(value[i], other[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, item := range value {
			otherItem, exists := other[key]
			if !exists || !equalWorkflowValue(item, otherItem) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

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
	RunID       string          `json:"run_id"`
	StepName    string          `json:"step_name"`
	Status      string          `json:"status"`
	Attempt     int             `json:"attempt"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	NextCheckAt *time.Time      `json:"next_check_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Error       *string         `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
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
	// CreateWorkflowRunAdmitted serializes quota admission per app and returns
	// the observed active count when the quota is already full.
	CreateWorkflowRunAdmitted(ctx context.Context, r *WorkflowRun, maxActive int) (active int, err error)
	GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error)
	ListWorkflowRuns(ctx context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error)
	MarkWorkflowRunStatus(ctx context.Context, id, status string, output json.RawMessage, lastErr *string) error
	ClaimNextPendingRun(ctx context.Context) (*WorkflowRun, error)
	// ClaimNextDueWorkflowRun claims a pending run or a parked wait whose
	// scheduled_for deadline has arrived. A due timer completes; a due
	// event wait takes its timeout path.
	ClaimNextDueWorkflowRun(ctx context.Context) (*WorkflowRun, error)
	ScheduleWorkflowRun(ctx context.Context, id, status string, scheduledFor time.Time) error
	// SetWorkflowRunWaitWake replaces a parked run's deadline after all active
	// waits have been evaluated. It does not override a newly pending event wake.
	SetWorkflowRunWaitWake(ctx context.Context, id string, scheduledFor time.Time) error
	RecoverWorkflowRun(ctx context.Context, id string) error
	CancelWorkflowRun(ctx context.Context, id, reason string) (*WorkflowRun, error)
	CountActiveRunsByApp(ctx context.Context, appID string) (int, error)

	// Steps
	CreateWorkflowSteps(ctx context.Context, runID string, steps []*WorkflowStep) error
	GetWorkflowSteps(ctx context.Context, runID string) ([]*WorkflowStep, error)
	MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error
	// ParkWorkflowTimer atomically records the step's first activation and the
	// run's durable wake deadline. Re-parking never resets the original deadline.
	ParkWorkflowTimer(ctx context.Context, runID, stepName string, duration time.Duration) (time.Time, error)
	// ResolveWorkflowCondition atomically records one checker result or expires
	// a parked condition. The run and step are locked in cancellation order.
	ResolveWorkflowCondition(ctx context.Context, update WorkflowConditionUpdate) (WorkflowConditionOutcome, error)
	// ParkWorkflowEvent atomically checks for an already-delivered event and
	// registers the wait. A concurrent event insertion cannot be lost between
	// the check and the park transition.
	ParkWorkflowEvent(ctx context.Context, runID, stepName, eventName string, timeout time.Duration) (*WorkflowEvent, time.Time, error)
	// ResolveWorkflowEventWait decides the deadline race while holding the
	// same run lock as event insertion and callback completion. If an event
	// exists it wins; otherwise an elapsed deadline closes the wait.
	ResolveWorkflowEventWait(ctx context.Context, runID, stepName, eventName string, timeout time.Duration, onTimeout bool) (*WorkflowEvent, bool, error)

	// Events
	InsertWorkflowEvent(ctx context.Context, e *WorkflowEvent) error
	// CompleteWorkflowCallback records one account-authorized completion. The
	// stable event ID makes retries idempotent; a different payload conflicts.
	CompleteWorkflowCallback(ctx context.Context, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (duplicate bool, err error)
	GetWorkflowEventsForRun(ctx context.Context, runID string) ([]*WorkflowEvent, error)
	FindMatchingEvent(ctx context.Context, runID, eventName string) (*WorkflowEvent, error)

	// Retention
	SweepExpiredWorkflowRuns(ctx context.Context, olderThan time.Duration) (int, error)
	SweepExpiredWorkflowEvents(ctx context.Context, olderThan time.Duration) (int, error)
}
