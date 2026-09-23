package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppTaskKind identifies why a deployment-attached command was admitted.
// Manual tasks are customer initiated; release tasks are an internal deploy
// gate and are unique per deployment.
type AppTaskKind string

const (
	AppTaskKindManual  AppTaskKind = "manual"
	AppTaskKindRelease AppTaskKind = "release"
)

func (k AppTaskKind) Valid() bool {
	return k == AppTaskKindManual || k == AppTaskKindRelease
}

// AppTaskStatus is the durable scheduler lifecycle from ADR-230.
type AppTaskStatus string

const (
	AppTaskQueued    AppTaskStatus = "queued"
	AppTaskRestoring AppTaskStatus = "restoring"
	AppTaskRunning   AppTaskStatus = "running"
	AppTaskSucceeded AppTaskStatus = "succeeded"
	AppTaskFailed    AppTaskStatus = "failed"
	AppTaskTimedOut  AppTaskStatus = "timed_out"
	AppTaskCancelled AppTaskStatus = "cancelled"
)

func (s AppTaskStatus) Terminal() bool {
	switch s {
	case AppTaskSucceeded, AppTaskFailed, AppTaskTimedOut, AppTaskCancelled:
		return true
	default:
		return false
	}
}

// AppTask is the payload-free control-plane projection of one command pinned
// to an immutable deployment artifact. Environment and secret values are
// resolved only by the scheduler immediately before the fresh VM boots.
type AppTask struct {
	ID              string
	AccountID       string
	AppID           string
	DeploymentID    string
	Kind            AppTaskKind
	Command         []string
	CommandShell    bool
	DeploymentScope string
	ArtifactKey     string
	ImageDigest     string
	Status          AppTaskStatus
	TimeoutSeconds  int
	MaxOutputBytes  int
	LeaseToken      *string
	LeaseOwner      *string
	LeaseExpiresAt  *time.Time
	CancelRequested *time.Time
	StdoutTail      string
	StderrTail      string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     *string
	FailureMessage  *string
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	AppTaskDefaultTimeoutSeconds = 600
	AppTaskDefaultMaxOutputBytes = 1024 * 1024
	AppTaskMaxCommandArgs        = 64
	AppTaskMaxCommandArgBytes    = 4096
	AppTaskMaxCommandBytes       = 16384
)

// CreateAppTaskParams is already-resolved app-task intent. Scope, artifact
// key, and image digest are copied atomically from DeploymentID by the store.
type CreateAppTaskParams struct {
	AccountID      string
	AppID          string
	DeploymentID   string
	Kind           AppTaskKind
	Command        []string
	CommandShell   bool
	TimeoutSeconds int
	MaxOutputBytes int
	CreatedAt      time.Time
}

// CompleteAppTaskParams is the scheduler-owned terminal compare-and-swap.
// LeaseToken prevents a stale scheduler from overwriting a task recovered by
// another owner.
type CompleteAppTaskParams struct {
	ID              string
	LeaseToken      string
	Status          AppTaskStatus
	StdoutTail      string
	StderrTail      string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     *string
	FailureMessage  *string
	FinishedAt      time.Time
}

// AppTaskSweepResult reports recovery work. Restoring tasks are safe to
// replay because running is the at-most-once dispatch fence.
type AppTaskSweepResult struct {
	RequeuedRestores int
	FailedRuns       int
	Cancelled        int
}

var (
	ErrAppTaskInvalid               = errors.New("state: invalid app task")
	ErrAppTaskDeploymentUnavailable = errors.New("state: app task deployment artifact is unavailable")
	ErrAppTaskLeaseLost             = errors.New("state: app task lease lost")
)

// AppTaskStore is deliberately separate from ExecutionStore: disposable
// source execution and deployment-attached commands have different security,
// network, environment, and artifact contracts.
type AppTaskStore interface {
	CreateAppTask(ctx context.Context, params CreateAppTaskParams) (AppTask, error)
	AppTaskByID(ctx context.Context, accountID, appID, taskID string) (AppTask, error)
	ListAppTasks(ctx context.Context, accountID, appID string, limit, offset int) ([]AppTask, error)
	ClaimNextAppTask(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (AppTask, error)
	MarkAppTaskRunning(ctx context.Context, taskID, leaseToken string, startedAt time.Time) (AppTask, error)
	RequestAppTaskCancellation(ctx context.Context, accountID, appID, taskID string, requestedAt time.Time) (AppTask, error)
	CompleteAppTask(ctx context.Context, params CompleteAppTaskParams) (AppTask, error)
	SweepExpiredAppTasks(ctx context.Context, at time.Time) (AppTaskSweepResult, error)
}

func resolveCreateAppTask(params CreateAppTaskParams) (CreateAppTaskParams, error) {
	if params.AccountID == "" || params.AppID == "" || params.DeploymentID == "" {
		return CreateAppTaskParams{}, fmt.Errorf("%w: account, app, and deployment are required", ErrAppTaskInvalid)
	}
	if !params.Kind.Valid() {
		return CreateAppTaskParams{}, fmt.Errorf("%w: unsupported kind %q", ErrAppTaskInvalid, params.Kind)
	}
	if err := validateAppTaskCommand(params.Command); err != nil {
		return CreateAppTaskParams{}, err
	}
	if params.CommandShell && len(params.Command) != 1 {
		return CreateAppTaskParams{}, fmt.Errorf("%w: shell commands must be stored as one command string", ErrAppTaskInvalid)
	}
	if params.TimeoutSeconds == 0 {
		params.TimeoutSeconds = AppTaskDefaultTimeoutSeconds
	}
	if params.TimeoutSeconds < 1 || params.TimeoutSeconds > 3600 {
		return CreateAppTaskParams{}, fmt.Errorf("%w: timeout_seconds must be between 1 and 3600", ErrAppTaskInvalid)
	}
	if params.MaxOutputBytes == 0 {
		params.MaxOutputBytes = AppTaskDefaultMaxOutputBytes
	}
	if params.MaxOutputBytes < 1024 || params.MaxOutputBytes > 16*1024*1024 {
		return CreateAppTaskParams{}, fmt.Errorf("%w: max_output_bytes must be between 1024 and 16777216", ErrAppTaskInvalid)
	}
	if params.CreatedAt.IsZero() {
		params.CreatedAt = time.Now().UTC()
	} else {
		params.CreatedAt = params.CreatedAt.UTC()
	}
	params.Command = append([]string(nil), params.Command...)
	return params, nil
}

func validateAppTaskCommand(command []string) error {
	if len(command) == 0 || len(command) > AppTaskMaxCommandArgs {
		return fmt.Errorf("%w: command must contain between 1 and %d arguments", ErrAppTaskInvalid, AppTaskMaxCommandArgs)
	}
	total := 0
	for i, arg := range command {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("%w: command argument %d contains NUL", ErrAppTaskInvalid, i)
		}
		if len(arg) > AppTaskMaxCommandArgBytes {
			return fmt.Errorf("%w: command argument %d exceeds %d bytes", ErrAppTaskInvalid, i, AppTaskMaxCommandArgBytes)
		}
		total += len(arg)
	}
	if strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("%w: command executable is required", ErrAppTaskInvalid)
	}
	if total > AppTaskMaxCommandBytes {
		return fmt.Errorf("%w: command exceeds %d bytes", ErrAppTaskInvalid, AppTaskMaxCommandBytes)
	}
	return nil
}

func validateCompleteAppTask(params CompleteAppTaskParams, maxOutputBytes int) error {
	if params.ID == "" || params.LeaseToken == "" || !params.Status.Terminal() {
		return fmt.Errorf("%w: id, lease token, and terminal status are required", ErrAppTaskInvalid)
	}
	if params.FinishedAt.IsZero() {
		return fmt.Errorf("%w: finished_at is required", ErrAppTaskInvalid)
	}
	if len(params.StdoutTail)+len(params.StderrTail) > maxOutputBytes {
		return fmt.Errorf("%w: terminal output exceeds admitted byte budget", ErrAppTaskInvalid)
	}
	if params.ExitCode != nil && (*params.ExitCode < 0 || *params.ExitCode > 255) {
		return fmt.Errorf("%w: exit code must be between 0 and 255", ErrAppTaskInvalid)
	}
	failurePresent := params.FailureCode != nil || params.FailureMessage != nil
	if (params.FailureCode == nil) != (params.FailureMessage == nil) {
		return fmt.Errorf("%w: failure code and message must be supplied together", ErrAppTaskInvalid)
	}
	if failurePresent && params.Status != AppTaskFailed && params.Status != AppTaskTimedOut {
		return fmt.Errorf("%w: only failed or timed-out tasks may store failure details", ErrAppTaskInvalid)
	}
	if params.Status == AppTaskSucceeded {
		if params.ExitCode == nil || *params.ExitCode != 0 || failurePresent {
			return fmt.Errorf("%w: succeeded task requires exit code 0 and no failure", ErrAppTaskInvalid)
		}
	}
	if params.Status == AppTaskFailed || params.Status == AppTaskTimedOut {
		if params.FailureCode == nil || len(*params.FailureCode) == 0 || len(*params.FailureCode) > 64 || len(*params.FailureMessage) > 4096 {
			return fmt.Errorf("%w: failed task requires bounded failure details", ErrAppTaskInvalid)
		}
	}
	return nil
}

func validAppTaskDeployment(d Deployment, appID string) bool {
	if d.AppID != appID || d.RootfsKey == "" || d.ImageDigest == "" {
		return false
	}
	switch d.Status {
	case DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded:
		return api.ValidateScope(normalizedDeploymentScope(d.Scope)) == nil
	default:
		return false
	}
}

func normalizeAppTaskPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func cloneAppTask(task AppTask) AppTask {
	task.Command = append([]string(nil), task.Command...)
	task.LeaseToken = cloneAppTaskStringPtr(task.LeaseToken)
	task.LeaseOwner = cloneAppTaskStringPtr(task.LeaseOwner)
	task.LeaseExpiresAt = cloneAppTaskTimePtr(task.LeaseExpiresAt)
	task.CancelRequested = cloneAppTaskTimePtr(task.CancelRequested)
	task.ExitCode = cloneAppTaskIntPtr(task.ExitCode)
	task.FailureCode = cloneAppTaskStringPtr(task.FailureCode)
	task.FailureMessage = cloneAppTaskStringPtr(task.FailureMessage)
	task.StartedAt = cloneAppTaskTimePtr(task.StartedAt)
	task.FinishedAt = cloneAppTaskTimePtr(task.FinishedAt)
	return task
}

func cloneAppTaskStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneAppTaskIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneAppTaskTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
