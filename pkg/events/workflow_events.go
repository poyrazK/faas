package events

import (
	"encoding/json"
	"time"
)

// Workflow audit event kinds (ADR-081 §8).
const (
	WorkflowStarted       = "app.workflow.started"
	WorkflowStepStarted   = "app.workflow.step_started"
	WorkflowStepSucceeded = "app.workflow.step_succeeded"
	WorkflowStepFailed    = "app.workflow.step_failed"
	WorkflowAwaitingEvent = "app.workflow.awaiting_event"
	WorkflowEventReceived = "app.workflow.event_received"
	WorkflowSucceeded     = "app.workflow.succeeded"
	WorkflowFailed        = "app.workflow.failed"
	WorkflowDeadLetter    = "app.workflow.dead_letter"
)

// WorkflowEventCommon carries shared correlation identifiers across workflow events.
type WorkflowEventCommon struct {
	RunID        string    `json:"run_id"`
	AppID        string    `json:"app_id"`
	WorkflowName string    `json:"workflow_name"`
	Timestamp    time.Time `json:"timestamp"`
}

// WorkflowStartedPayload is emitted when a new workflow run is accepted into pending/running.
type WorkflowStartedPayload struct {
	WorkflowEventCommon
	Input json.RawMessage `json:"input,omitempty"`
}

// WorkflowStepPayload is emitted when a step is started, succeeded, or failed.
type WorkflowStepPayload struct {
	WorkflowEventCommon
	StepName string          `json:"step_name"`
	Attempt  int             `json:"attempt"`
	Status   string          `json:"status"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// WorkflowAwaitingEventPayload is emitted when a wait_for_event step parks the run.
type WorkflowAwaitingEventPayload struct {
	WorkflowEventCommon
	StepName  string `json:"step_name"`
	EventName string `json:"event_name"`
	Timeout   string `json:"timeout"`
}

// WorkflowEventReceivedPayload is emitted when an external event matches an awaiting run.
type WorkflowEventReceivedPayload struct {
	WorkflowEventCommon
	EventName string          `json:"event_name"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// WorkflowCompletedPayload is emitted when a workflow run transitions to succeeded, failed, or dead.
type WorkflowCompletedPayload struct {
	WorkflowEventCommon
	Outcome string          `json:"outcome"`
	Output  json.RawMessage `json:"output,omitempty"`
	Error   string          `json:"error,omitempty"`
}
