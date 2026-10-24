package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// WorkflowCodeStore records code-first workflow invocations
// (ADR-20261021204307). Every call takes the run lock and checks the run's
// generation fence, like the declarative step setters.
type WorkflowCodeStore interface {
	// BeginWorkflowCodeInvocation creates the run's $driver row on first use,
	// starts its next attempt and marks the run running. It returns the
	// attempt number the caller must pass to FinishWorkflowCodeInvocation.
	BeginWorkflowCodeInvocation(ctx context.Context, runID string, driverInput json.RawMessage) (int, error)
	// FinishWorkflowCodeInvocation applies one entry-handler response in one
	// transaction: the operation rows and their attempts, the $driver attempt
	// and the $driver row's next state. The $driver row stays pending; a
	// finished function's result is kept in its output until the run closes.
	FinishWorkflowCodeInvocation(ctx context.Context, runID string, finish WorkflowCodeInvocation) error
	// ParkWorkflowCodeEvent resolves or parks an event wait. The wait takes
	// the run's earliest event of that name, received before its deadline,
	// that no other wait of the run consumed; the step is completed with the
	// payload and records the event's ID in its descriptor. Past the
	// deadline the step succeeds with no output and consumes nothing. Else
	// the step and run park until the deadline.
	ParkWorkflowCodeEvent(ctx context.Context, runID, stepName, eventName string, timeout time.Duration) (*WorkflowEvent, bool, time.Time, error)
}

// workflowCodeEventWait is the part of a code operation descriptor that
// event matching reads (pkg/workflowcode owns the full shape).
type workflowCodeEventWait struct {
	Kind    string `json:"kind"`
	Event   string `json:"event"`
	EventID string `json:"event_id"`
}

func decodeWorkflowCodeEventWait(input json.RawMessage) (workflowCodeEventWait, bool) {
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(input, &wrapper) != nil || wrapper[api.WorkflowCodeOpKey] == nil {
		return workflowCodeEventWait{}, false
	}
	var wait workflowCodeEventWait
	if json.Unmarshal(wrapper[api.WorkflowCodeOpKey], &wait) != nil || wait.Kind != "event" {
		return workflowCodeEventWait{}, false
	}
	return wait, true
}

// withWorkflowCodeEventID records the consumed event in a wait's descriptor.
func withWorkflowCodeEventID(input json.RawMessage, eventID string) (json.RawMessage, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(input, &wrapper); err != nil {
		return nil, err
	}
	var descriptor map[string]json.RawMessage
	if err := json.Unmarshal(wrapper[api.WorkflowCodeOpKey], &descriptor); err != nil {
		return nil, err
	}
	id, err := json.Marshal(eventID)
	if err != nil {
		return nil, err
	}
	descriptor["event_id"] = id
	if wrapper[api.WorkflowCodeOpKey], err = json.Marshal(descriptor); err != nil {
		return nil, err
	}
	return json.Marshal(wrapper)
}

// WorkflowCodeOpWrite is one operation row change.
type WorkflowCodeOpWrite struct {
	StepName string
	// New inserts the row with Input; otherwise the existing non-terminal
	// row is updated.
	New   bool
	Input json.RawMessage
	// Status is succeeded or failed for a finished step attempt, pending for
	// a step retry or a new wait.
	Status string
	// Attempt > 0 records that step attempt in workflow_step_attempts.
	Attempt int
	Output  json.RawMessage
	Error   *string
	RetryAt *time.Time
}

// WorkflowCodeInvocation is one entry-handler call's outcome.
type WorkflowCodeInvocation struct {
	Attempt    int
	HTTPStatus *int
	// Error marks a failed call; RetryAt is its retry deadline.
	Error   *string
	RetryAt *time.Time
	Ops     []WorkflowCodeOpWrite
	// DriverInput replaces the $driver descriptor when set.
	DriverInput json.RawMessage
	// Result keeps a finished function's result on the $driver row.
	Result json.RawMessage
}

func validateWorkflowCodeInvocation(finish WorkflowCodeInvocation) error {
	if finish.Attempt < 1 {
		return ErrWorkflowInvalidAttempt
	}
	if err := validateWorkflowHTTPStatus(finish.HTTPStatus); err != nil {
		return err
	}
	if finish.RetryAt != nil && finish.Error == nil {
		return fmt.Errorf("%w: invocation retry without an error", ErrWorkflowInvalidRecord)
	}
	if err := validateWorkflowJSON(finish.DriverInput, false); err != nil {
		return err
	}
	if err := validateWorkflowJSON(finish.Result, false); err != nil {
		return err
	}
	for _, op := range finish.Ops {
		if op.StepName == "" || op.StepName == api.WorkflowCodeDriverStep {
			return fmt.Errorf("%w: invalid code operation name", ErrWorkflowInvalidRecord)
		}
		switch op.Status {
		case WorkflowStepStatusSucceeded, WorkflowStepStatusFailed:
			if op.Attempt < 1 || op.RetryAt != nil {
				return fmt.Errorf("%w: finished step %q needs an attempt and no retry", ErrWorkflowInvalidRecord, op.StepName)
			}
		case WorkflowStepStatusPending:
			if (op.Attempt > 0) != (op.RetryAt != nil) {
				return fmt.Errorf("%w: pending operation %q needs an attempt exactly when it retries", ErrWorkflowInvalidRecord, op.StepName)
			}
		default:
			return fmt.Errorf("%w: code operation status %q", ErrWorkflowInvalidStatus, op.Status)
		}
		if op.New {
			if err := validateWorkflowJSON(op.Input, true); err != nil {
				return err
			}
		}
		if err := validateWorkflowJSON(op.Output, false); err != nil {
			return err
		}
	}
	return nil
}

// workflowCodeAttemptStatus maps an operation write to its attempt status.
func workflowCodeAttemptStatus(op WorkflowCodeOpWrite) string {
	if op.Status == WorkflowStepStatusSucceeded {
		return WorkflowAttemptStatusSucceeded
	}
	return WorkflowAttemptStatusFailed
}

func workflowCodeTerminalStatus(status string) bool {
	return status == WorkflowStepStatusSucceeded || status == WorkflowStepStatusFailed ||
		status == WorkflowStepStatusDead || status == WorkflowStepStatusSkipped
}
