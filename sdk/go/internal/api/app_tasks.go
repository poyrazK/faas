package api

// AppTaskKind identifies the source of a deployment-attached command.
type AppTaskKind string

const (
	AppTaskKindManual  AppTaskKind = "manual"
	AppTaskKindRelease AppTaskKind = "release"
	AppTaskKindCron    AppTaskKind = "cron"
)

// AppTaskStatus is the durable lifecycle of a deployment-attached command.
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

func (s AppTaskStatus) Terminal() bool {
	switch s {
	case AppTaskStatusSucceeded, AppTaskStatusFailed, AppTaskStatusTimedOut, AppTaskStatusCancelled:
		return true
	default:
		return false
	}
}

type CreateAppTaskRequest struct {
	Command        []string `json:"command"`
	CommandShell   bool     `json:"command_shell,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
}

type AppTaskFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

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
