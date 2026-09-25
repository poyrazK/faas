package api

import (
	"fmt"
	"net/http"
	"strings"
)

// AppTaskKind identifies why a deployment-attached command runs. Direct API
// admission creates manual tasks; release and cron tasks are scheduler-owned.
type AppTaskKind string

const (
	AppTaskKindManual  AppTaskKind = "manual"
	AppTaskKindRelease AppTaskKind = "release"
	AppTaskKindCron    AppTaskKind = "cron"
)

// AppTaskStatus is the customer-visible lifecycle for a deployment-attached
// one-off command.
type AppTaskStatus string

const (
	AppTaskStatusQueued    AppTaskStatus = "queued"
	AppTaskStatusRestoring AppTaskStatus = "restoring"
	AppTaskStatusRunning   AppTaskStatus = "running"
	AppTaskStatusSucceeded AppTaskStatus = "succeeded"
	AppTaskStatusFailed    AppTaskStatus = "failed"
	AppTaskStatusTimedOut  AppTaskStatus = "timed_out"
	AppTaskStatusCancelled AppTaskStatus = "cancelled"
)

// Terminal reports whether the task can no longer change lifecycle state.
func (s AppTaskStatus) Terminal() bool {
	switch s {
	case AppTaskStatusSucceeded, AppTaskStatusFailed, AppTaskStatusTimedOut, AppTaskStatusCancelled:
		return true
	default:
		return false
	}
}

const (
	AppTaskDefaultTimeoutSeconds = 600
	AppTaskDefaultMaxOutputBytes = 1024 * 1024
	// AppTaskServiceBindingProbeCommand is a reserved argv[0] handled directly
	// by guest-init. It runs a bounded HTTPS canary without requiring utilities
	// or a language runtime in the customer image.
	AppTaskServiceBindingProbeCommand = "__gregale_service_binding_probe_v1__"
	AppTaskMaxCommandArgs             = 64
	AppTaskMaxCommandArgBytes         = 4096
	AppTaskMaxCommandBytes            = 16384
	// AppTaskRequestMaxBytes permits JSON escaping overhead while the decoded
	// command remains bounded by AppTaskMaxCommandBytes.
	AppTaskRequestMaxBytes int64 = 128 * 1024
)

// CreateAppTaskRequest is the public admission contract. Kind and deployment
// are not caller-selected: public requests are always manual and apid pins the
// app's current live deployment before persistence. The reserved
// AppTaskServiceBindingProbeCommand argv[0] is handled by guest-init for the
// `gregale bindings verify` operation and is not an image executable.
type CreateAppTaskRequest struct {
	Command        []string `json:"command"`
	CommandShell   bool     `json:"command_shell,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
}

// ResolvedCreateAppTaskRequest contains validated limits and defensive copies
// suitable for the state admission boundary.
type ResolvedCreateAppTaskRequest struct {
	Command        []string
	CommandShell   bool
	TimeoutSeconds int
	MaxOutputBytes int
}

// Resolve validates all caller-controlled fields and fills bounded defaults.
func (r CreateAppTaskRequest) Resolve() (ResolvedCreateAppTaskRequest, *Problem) {
	if len(r.Command) == 0 || len(r.Command) > AppTaskMaxCommandArgs {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid(
			fmt.Sprintf("command must contain between 1 and %d arguments", AppTaskMaxCommandArgs),
		)
	}
	total := 0
	for i, arg := range r.Command {
		if strings.ContainsRune(arg, '\x00') {
			return ResolvedCreateAppTaskRequest{}, appTaskInvalid(fmt.Sprintf("command argument %d contains NUL", i))
		}
		if len(arg) > AppTaskMaxCommandArgBytes {
			return ResolvedCreateAppTaskRequest{}, appTaskInvalid(
				fmt.Sprintf("command argument %d exceeds %d bytes", i, AppTaskMaxCommandArgBytes),
			)
		}
		total += len(arg)
	}
	if strings.TrimSpace(r.Command[0]) == "" {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("command executable is required")
	}
	if total > AppTaskMaxCommandBytes {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid(
			fmt.Sprintf("command exceeds %d bytes", AppTaskMaxCommandBytes),
		)
	}
	if r.CommandShell && len(r.Command) != 1 {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("command_shell requires exactly one command string")
	}

	timeoutSeconds := r.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = AppTaskDefaultTimeoutSeconds
	}
	if timeoutSeconds < 1 || timeoutSeconds > 3600 {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("timeout_seconds must be between 1 and 3600")
	}
	maxOutputBytes := r.MaxOutputBytes
	if maxOutputBytes == 0 {
		maxOutputBytes = AppTaskDefaultMaxOutputBytes
	}
	if maxOutputBytes < 1024 || maxOutputBytes > 16*1024*1024 {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("max_output_bytes must be between 1024 and 16777216")
	}

	return ResolvedCreateAppTaskRequest{
		Command:        append([]string(nil), r.Command...),
		CommandShell:   r.CommandShell,
		TimeoutSeconds: timeoutSeconds,
		MaxOutputBytes: maxOutputBytes,
	}, nil
}

func appTaskInvalid(detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeValidation, "Invalid app task", detail)
}

// AppTaskFailure is present for a queued retry's latest failed attempt, or a
// task's terminal failed/timed-out outcome.
type AppTaskFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AppTaskResponse deliberately excludes scheduler leases, artifact storage
// keys, and image digests. DeploymentID is sufficient for a customer to
// identify the immutable release that was selected at admission.
type AppTaskResponse struct {
	ID                  string          `json:"id"`
	AppID               string          `json:"app_id"`
	DeploymentID        string          `json:"deployment_id"`
	DeploymentScope     string          `json:"deployment_scope"`
	Kind                AppTaskKind     `json:"kind"`
	Command             []string        `json:"command"`
	CommandShell        bool            `json:"command_shell"`
	Status              AppTaskStatus   `json:"status"`
	TimeoutSeconds      int             `json:"timeout_seconds"`
	MaxOutputBytes      int             `json:"max_output_bytes"`
	RetryMax            int             `json:"retry_max,omitempty"`
	RetryBackoffSeconds int             `json:"retry_backoff_seconds,omitempty"`
	AttemptCount        int             `json:"attempt_count"`
	RetryAt             *string         `json:"retry_at,omitempty"`
	StdoutTail          string          `json:"stdout_tail,omitempty"`
	StderrTail          string          `json:"stderr_tail,omitempty"`
	OutputTruncated     bool            `json:"output_truncated"`
	ExitCode            *int            `json:"exit_code,omitempty"`
	Failure             *AppTaskFailure `json:"failure,omitempty"`
	CancelRequestedAt   *string         `json:"cancel_requested_at,omitempty"`
	StartedAt           *string         `json:"started_at,omitempty"`
	FinishedAt          *string         `json:"finished_at,omitempty"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
}

type AppTaskListResponse struct {
	Tasks      []AppTaskResponse `json:"tasks"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
	NextOffset int               `json:"next_offset"`
}
