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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
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

const (
	WorkflowAttemptStatusRunning   = "running"
	WorkflowAttemptStatusRetrying  = "retrying"
	WorkflowAttemptStatusSucceeded = "succeeded"
	WorkflowAttemptStatusFailed    = "failed"
)

var (
	ErrWorkflowRunNotFound       = errors.New("state: workflow run not found")
	ErrWorkflowStepNotFound      = errors.New("state: workflow step not found")
	ErrWorkflowAttemptNotFound   = errors.New("state: workflow step attempt not found")
	ErrWorkflowEventNotFound     = errors.New("state: workflow event not found")
	ErrWorkflowNotRunning        = errors.New("state: workflow run is not in running state")
	ErrWorkflowInvalidStatus     = errors.New("state: invalid workflow status")
	ErrWorkflowInvalidAttempt    = errors.New("state: workflow attempt cannot be negative")
	ErrWorkflowInvalidPagination = errors.New("state: workflow pagination cannot be negative")
	ErrWorkflowInvalidInput      = errors.New("state: workflow JSON payload is invalid")
	ErrWorkflowInvalidRecord     = errors.New("state: workflow record is invalid")
	ErrWorkflowRunQuotaExceeded  = errors.New("state: workflow active-run quota exceeded")
	ErrWorkflowRetryNotAllowed   = errors.New("state: workflow step retry is not allowed")
	ErrWorkflowCallbackClosed    = errors.New("state: workflow callback is closed")
	ErrWorkflowCallbackExpired   = errors.New("state: workflow callback has expired")
)

// WorkflowRunStaleAfter is the fallback for runs claimed before leases were
// introduced. New claims get a five-minute lease, extended to the declared
// step timeout plus five minutes before each executor call.
const WorkflowRunStaleAfter = 2*time.Hour + 5*time.Minute

// WorkflowRunLeaseStore is implemented by production stores. It lets a
// dispatcher bound crash recovery to the actual step timeout instead of the
// largest timeout supported by any plan.
type WorkflowRunLeaseStore interface {
	ExtendWorkflowRunLease(context.Context, string, time.Duration) error
}

// WorkflowRetryStore requeues a terminal run around one failed HTTP step.
// It remains optional so existing WorkflowStore implementations and test
// doubles do not need to support the operator retry surface.
type WorkflowRetryStore interface {
	RetryWorkflowStep(context.Context, string, string, int) (*WorkflowRun, int, error)
}

// workflowRetryPlan validates an in-place retry and returns skipped dependency
// descendants that must be reopened so the DAG can continue after the retry.
func workflowRetryPlan(run *WorkflowRun, stepName string, steps []*WorkflowStep) ([]string, error) {
	if run == nil || (run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead) {
		return nil, ErrWorkflowRetryNotAllowed
	}
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return nil, ErrWorkflowRetryNotAllowed
	}
	var target *api.WorkflowStepSpec
	for i := range spec.Steps {
		step := spec.Steps[i]
		if step.Name == stepName {
			target = &spec.Steps[i]
		}
	}
	if target == nil || (target.Run == "" && target.Path == "") {
		return nil, ErrWorkflowRetryNotAllowed
	}
	if len(steps) == 0 {
		return nil, ErrWorkflowRetryNotAllowed
	}
	stepByName := make(map[string]*WorkflowStep, len(steps))
	for _, step := range steps {
		if step != nil {
			stepByName[step.StepName] = step
		}
	}
	failed, exists := stepByName[stepName]
	if !exists || (failed.Status != WorkflowStepStatusFailed && failed.Status != WorkflowStepStatusDead) {
		return nil, ErrWorkflowRetryNotAllowed
	}
	for name, step := range stepByName {
		if name == stepName {
			continue
		}
		if step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead ||
			step.Status == WorkflowStepStatusRunning || step.Status == WorkflowStepStatusAwaitingEvent {
			return nil, ErrWorkflowRetryNotAllowed
		}
		// Cancellation marks skipped steps with the run's last error. Do not
		// turn an operator-cancelled run back into active work.
		if run.LastError != nil && step.Status == WorkflowStepStatusSkipped && step.Error != nil && *step.Error == *run.LastError {
			return nil, ErrWorkflowRetryNotAllowed
		}
	}
	for _, candidate := range spec.Steps {
		if candidate.OnFailure == stepName || candidate.OnTimeout == stepName {
			return nil, ErrWorkflowRetryNotAllowed
		}
	}
	for _, handlerName := range []string{target.OnFailure, target.OnTimeout} {
		if handlerName == "" {
			continue
		}
		handler := stepByName[handlerName]
		if handler != nil && handler.Status == WorkflowStepStatusSucceeded {
			return nil, ErrWorkflowRetryNotAllowed
		}
	}

	// Descendants reached through depends_on are rerun if they were skipped
	// because this step failed. A completed descendant means its effects have
	// already escaped and cannot safely be replayed in place.
	children := make(map[string][]string, len(spec.Steps))
	for _, candidate := range spec.Steps {
		for _, dependency := range candidate.DependsOn {
			children[dependency] = append(children[dependency], candidate.Name)
		}
	}
	var reopen []string
	visited := map[string]bool{stepName: true}
	queue := []string{stepName}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if visited[child] {
				continue
			}
			visited[child] = true
			queue = append(queue, child)
			step := stepByName[child]
			if step == nil {
				return nil, ErrWorkflowRetryNotAllowed
			}
			if step.Status == WorkflowStepStatusSucceeded {
				return nil, ErrWorkflowRetryNotAllowed
			}
			if step.Status == WorkflowStepStatusSkipped {
				reopen = append(reopen, child)
			}
		}
	}
	return reopen, nil
}

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

func validateWorkflowHTTPStatus(status *int) error {
	if status != nil && (*status < 100 || *status > 599) {
		return fmt.Errorf("%w: HTTP status must be between 100 and 599", ErrWorkflowInvalidRecord)
	}
	return nil
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

func cloneWorkflowInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneWorkflowTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneWorkflowString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
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
	ResumeCount        int             `json:"resume_count"`
	CancelledAt        *time.Time      `json:"cancelled_at,omitempty"`
	ID                 string          `json:"id"`
	AppID              string          `json:"app_id"`
	PlatformTenantID   string          `json:"platform_tenant_id,omitempty"`
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

func earlierWorkflowWake(current, candidate, now time.Time) time.Time {
	if current.After(now) && (candidate.IsZero() || current.Before(candidate)) {
		return current
	}
	return candidate
}

// WorkflowStep is one row of public.workflow_steps.
type WorkflowStep struct {
	RetryBase            int        `json:"retry_base"`
	ForEachParent        *string    `json:"for_each_parent,omitempty"`
	ForEachIndex         *int       `json:"for_each_index,omitempty"`
	ForEachCount         *int       `json:"for_each_count,omitempty"`
	WhenMatched          *bool      `json:"when_matched,omitempty"`
	WhenEvaluatedAt      *time.Time `json:"when_evaluated_at,omitempty"`
	SkipReason           *string    `json:"skip_reason,omitempty"`
	outboundAttemptToken string
	RunID                string          `json:"run_id"`
	StepName             string          `json:"step_name"`
	Status               string          `json:"status"`
	Attempt              int             `json:"attempt"`
	Input                json.RawMessage `json:"input,omitempty"`
	Output               json.RawMessage `json:"output,omitempty"`
	StartedAt            *time.Time      `json:"started_at,omitempty"`
	NextCheckAt          *time.Time      `json:"next_check_at,omitempty"`
	NextRetryAt          *time.Time      `json:"next_retry_at,omitempty"`
	FinishedAt           *time.Time      `json:"finished_at,omitempty"`
	Error                *string         `json:"error,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

// WorkflowStepAttempt is one executor invocation for a workflow step. Unlike
// WorkflowStep, attempts are append-only by (run, step, attempt) so retries
// remain inspectable after the step summary advances.
type WorkflowStepAttempt struct {
	RunID         string                      `json:"run_id"`
	StepName      string                      `json:"step_name"`
	Attempt       int                         `json:"attempt"`
	Status        string                      `json:"status"`
	HTTPStatus    *int                        `json:"http_status,omitempty"`
	StartedAt     time.Time                   `json:"started_at"`
	FinishedAt    *time.Time                  `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time                  `json:"next_attempt_at,omitempty"`
	Error         *string                     `json:"error,omitempty"`
	Effects       []api.OperationEffectRecord `json:"effects,omitempty"`
}

// ManagedWorkflowStepCommit is the accepted result of a managed HTTP step.
// The result, webhook intent, delivery rows, and step success share one
// platform transaction after the customer SDK has committed its receipt.
type ManagedWorkflowStepCommit struct {
	RunID       string
	StepName    string
	OperationID string
	Attempt     int
	HTTPStatus  int
	Output      json.RawMessage
	Effects     []exclusivework.Effect
}

// ManagedWorkflowStepCommitter is optional so existing workflow store doubles
// remain source-compatible. Production stores implement the atomic transition.
type ManagedWorkflowStepCommitter interface {
	CommitManagedWorkflowStep(context.Context, ManagedWorkflowStepCommit) error
}

type workflowStepAttemptKey struct {
	runID, stepName string
	attempt         int
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
	// SetWorkflowRunWake replaces the scheduler wake after active waits and
	// pending retries have been evaluated.
	SetWorkflowRunWake(ctx context.Context, id, status string, scheduledFor time.Time) error
	RecoverWorkflowRun(ctx context.Context, id string) error
	CancelWorkflowRun(ctx context.Context, id, reason string) (*WorkflowRun, error)
	CountActiveRunsByApp(ctx context.Context, appID string) (int, error)

	// Steps
	ResolveWorkflowForEach(ctx context.Context, runID, stepName string) (WorkflowForEachOutcome, error)
	// ResolveWorkflowStepJoin atomically closes a ready join and snapshots its selected output.
	// A false ready result means dependencies are still in progress.
	ResolveWorkflowStepJoin(ctx context.Context, runID, stepName string) (ready bool, err error)
	ResolveWorkflowStepGuard(ctx context.Context, runID, stepName string) (bool, error)
	SkipWorkflowStep(ctx context.Context, runID, stepName, reason string) error
	CreateWorkflowSteps(ctx context.Context, runID string, steps []*WorkflowStep) error
	GetWorkflowSteps(ctx context.Context, runID string) ([]*WorkflowStep, error)
	// StartWorkflowStep atomically persists the resolved input and transitions
	// a pending step to running, returning the stored representation so the
	// first dispatch and retries use the exact same payload.
	StartWorkflowStep(ctx context.Context, runID, stepName string, attempt int, input json.RawMessage) (json.RawMessage, error)
	MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error
	// MarkWorkflowStepAttemptStatus atomically updates a dispatched step and
	// closes its attempt with the executor's HTTP result.
	MarkWorkflowStepAttemptStatus(ctx context.Context, runID, stepName, status string, attempt int, httpStatus *int, output json.RawMessage, err *string) error
	// ScheduleWorkflowStepRetry atomically persists the retry deadline and run wake.
	ScheduleWorkflowStepRetry(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, stepErr string) error
	ScheduleWorkflowStepRetryWithHTTPStatus(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, httpStatus *int, stepErr string) error
	GetWorkflowStepAttempts(ctx context.Context, runID, stepName string) ([]*WorkflowStepAttempt, error)
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
