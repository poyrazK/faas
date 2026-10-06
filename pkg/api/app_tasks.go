package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/workpolicy"
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
	// AppTaskPostgresBindingProbeCommand is a reserved argv[0] handled directly
	// by guest-init. It runs a bounded, read-only PostgreSQL connectivity canary
	// using an environment key injected into the customer's deployment.
	AppTaskPostgresBindingProbeCommand = "__gregale_postgres_binding_probe_v1__"
	// AppTaskObjectStorageBindingProbeCommand runs a bounded, read-only S3
	// canary using the selected binding's six injected environment variables.
	AppTaskObjectStorageBindingProbeCommand = "__gregale_object_storage_binding_probe_v1__"
	AppTaskOutboundBindingProbeCommand      = "__gregale_outbound_binding_probe_v1__"
	// AppTaskServiceBindingSmokeCommand is a reserved argv[0] handled directly
	// by guest-init. It sends one bounded GET through a declared HTTPS service
	// binding to an explicitly pinned target deployment.
	AppTaskServiceBindingSmokeCommand = "__gregale_service_binding_smoke_v1__"
	AppTaskMaxCommandArgs             = 64
	AppTaskMaxCommandArgBytes         = 4096
	AppTaskMaxCommandBytes            = 16384
	// AppTaskRequestMaxBytes permits JSON escaping overhead while the decoded
	// command remains bounded by AppTaskMaxCommandBytes.
	AppTaskRequestMaxBytes int64 = 128 * 1024
)

// CreateAppTaskRequest is the public admission contract. Public requests are
// always manual. Reserved binding probes and service smoke commands may select
// a live deployment explicitly through separate, command-specific selectors.
// The reserved
// AppTaskServiceBindingProbeCommand, AppTaskPostgresBindingProbeCommand,
// AppTaskObjectStorageBindingProbeCommand, and
// AppTaskServiceBindingSmokeCommand argv[0] are handled by guest-init for
// `gregale bindings verify`, its `--postgres` and `--object-storage` forms, and
// `gregale bindings smoke`; none is an image executable.
type CreateAppTaskRequest struct {
	VerificationDeploymentID string   `json:"verification_deployment_id,omitempty"`
	SmokeDeploymentID        string   `json:"smoke_deployment_id,omitempty"`
	Command                  []string `json:"command"`
	CommandShell             bool     `json:"command_shell,omitempty"`
	TimeoutSeconds           int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes           int      `json:"max_output_bytes,omitempty"`
}

// ResolvedCreateAppTaskRequest contains validated limits and defensive copies
// suitable for the state admission boundary.
type ResolvedCreateAppTaskRequest struct {
	VerificationDeploymentID string
	SmokeDeploymentID        string
	Command                  []string
	CommandShell             bool
	TimeoutSeconds           int
	MaxOutputBytes           int
}

// Resolve validates all caller-controlled fields and fills bounded defaults.
func (r CreateAppTaskRequest) Resolve() (ResolvedCreateAppTaskRequest, *Problem) {
	if r.VerificationDeploymentID != "" && r.SmokeDeploymentID != "" {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("verification_deployment_id and smoke_deployment_id are mutually exclusive")
	}
	smokeDeploymentID := r.SmokeDeploymentID
	if smokeDeploymentID != "" {
		parsed, err := uuid.Parse(smokeDeploymentID)
		if err != nil || parsed == uuid.Nil || !IsServiceBindingSmokeCommand(r.Command, r.CommandShell) {
			return ResolvedCreateAppTaskRequest{}, appTaskInvalid("smoke_deployment_id requires a deployment UUID and a reserved service binding smoke command")
		}
		smokeDeploymentID = parsed.String()
	}
	if len(r.Command) > 0 && r.Command[0] == AppTaskServiceBindingSmokeCommand && !IsServiceBindingSmokeCommand(r.Command, r.CommandShell) {
		return ResolvedCreateAppTaskRequest{}, appTaskInvalid("invalid service binding smoke command")
	}
	deploymentID := r.VerificationDeploymentID
	if deploymentID != "" {
		parsed, err := uuid.Parse(deploymentID)
		if err != nil || !IsBindingVerificationCommand(r.Command, r.CommandShell) {
			return ResolvedCreateAppTaskRequest{}, appTaskInvalid("verification_deployment_id requires a deployment UUID and a reserved binding verification probe")
		}
		deploymentID = parsed.String()
	}
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
		VerificationDeploymentID: deploymentID,
		SmokeDeploymentID:        smokeDeploymentID,
		Command:                  append([]string(nil), r.Command...),
		CommandShell:             r.CommandShell,
		TimeoutSeconds:           timeoutSeconds,
		MaxOutputBytes:           maxOutputBytes,
	}, nil
}

// IsBindingVerificationCommand excludes generic commands and service smoke
// requests; explicit deployment selection is limited to these four probes.
func IsBindingVerificationCommand(command []string, shell bool) bool {
	if !shell && len(command) == 3 && command[0] == AppTaskServiceBindingProbeCommand && command[1] != "" {
		id, err := uuid.Parse(command[2])
		return err == nil && id != uuid.Nil && id.String() == command[2]
	}
	if shell || len(command) != 2 || command[1] == "" {
		return false
	}
	switch command[0] {
	case AppTaskServiceBindingProbeCommand, AppTaskPostgresBindingProbeCommand, AppTaskObjectStorageBindingProbeCommand, AppTaskOutboundBindingProbeCommand:
		return true
	default:
		return false
	}
}

// IsServiceBindingSmokeCommand admits only a bounded platform GET with an exact
// target, canonical service name, origin-form path and explicit status policy.
func IsServiceBindingSmokeCommand(command []string, shell bool) bool {
	if shell || len(command) != 5 || command[0] != AppTaskServiceBindingSmokeCommand {
		return false
	}
	names, err := NormalizeServiceBindingTargets([]string{command[1]})
	if err != nil || len(names) != 1 || names[0] != command[1] {
		return false
	}
	id, err := uuid.Parse(command[2])
	if err != nil || id == uuid.Nil {
		return false
	}
	if _, err := NormalizeServiceBindingSmokePath(command[3]); err != nil {
		return false
	}
	status, err := strconv.Atoi(command[4])
	return err == nil && (status == 0 || status >= 200 && status <= 599)
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
	WorkDecision        *workpolicy.Decision `json:"work_decision,omitempty"`
	OutcomeCode         string               `json:"outcome_code,omitempty"`
	ID                  string               `json:"id"`
	AppID               string               `json:"app_id"`
	DeploymentID        string               `json:"deployment_id"`
	DeploymentScope     string               `json:"deployment_scope"`
	Kind                AppTaskKind          `json:"kind"`
	Command             []string             `json:"command"`
	CommandShell        bool                 `json:"command_shell"`
	Status              AppTaskStatus        `json:"status"`
	TimeoutSeconds      int                  `json:"timeout_seconds"`
	MaxOutputBytes      int                  `json:"max_output_bytes"`
	RetryMax            int                  `json:"retry_max,omitempty"`
	RetryBackoffSeconds int                  `json:"retry_backoff_seconds,omitempty"`
	AttemptCount        int                  `json:"attempt_count"`
	RetryAt             *string              `json:"retry_at,omitempty"`
	StdoutTail          string               `json:"stdout_tail,omitempty"`
	StderrTail          string               `json:"stderr_tail,omitempty"`
	OutputTruncated     bool                 `json:"output_truncated"`
	ExitCode            *int                 `json:"exit_code,omitempty"`
	Failure             *AppTaskFailure      `json:"failure,omitempty"`
	CancelRequestedAt   *string              `json:"cancel_requested_at,omitempty"`
	StartedAt           *string              `json:"started_at,omitempty"`
	FinishedAt          *string              `json:"finished_at,omitempty"`
	CreatedAt           string               `json:"created_at"`
	UpdatedAt           string               `json:"updated_at"`
}

type AppTaskListResponse struct {
	Tasks      []AppTaskResponse `json:"tasks"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
	NextOffset int               `json:"next_offset"`
}
