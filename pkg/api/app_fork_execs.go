package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CreateAppForkExecRequest is POST /v1/apps/{slug}/forks/{id}/execs
// (ADR-732): one command to run inside the running fork. Command is an argv;
// with Shell it is one string run by the app's shell.
type CreateAppForkExecRequest struct {
	Command        []string `json:"command"`
	Shell          bool     `json:"shell,omitempty"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"`
	MaxOutputBytes *int     `json:"max_output_bytes,omitempty"`
}

// AppForkExecRequestMaxBytes bounds the create body.
const AppForkExecRequestMaxBytes = 32 << 10

// Resolve validates the request and applies defaults.
func (r CreateAppForkExecRequest) Resolve() (timeoutSeconds, maxOutput int, problem *Problem) {
	total := 0
	for _, arg := range r.Command {
		total += len(arg)
		if strings.ContainsRune(arg, '\x00') || len(arg) > 4096 {
			return 0, 0, ErrValidation("command arguments must be at most 4096 bytes and contain no NUL")
		}
	}
	switch {
	case len(r.Command) == 0 || len(r.Command) > 64 || strings.TrimSpace(r.Command[0]) == "" || total > 16384:
		return 0, 0, ErrValidation("command must have 1 to 64 arguments and at most 16384 bytes")
	case r.Shell && len(r.Command) != 1:
		return 0, 0, ErrValidation("a shell command is exactly one string")
	}
	timeoutSeconds = int(AppForkExecDefaultTimeout / time.Second)
	if r.TimeoutSeconds != nil {
		timeoutSeconds = *r.TimeoutSeconds
	}
	if maxS := int(AppForkExecMaxTimeout / time.Second); timeoutSeconds < 1 || timeoutSeconds > maxS {
		return 0, 0, ErrValidation(fmt.Sprintf("timeout_seconds must be between 1 and %d", maxS))
	}
	maxOutput = AppForkExecDefaultOutputBytes
	if r.MaxOutputBytes != nil {
		maxOutput = *r.MaxOutputBytes
	}
	if maxOutput < 1024 || maxOutput > AppForkExecMaxOutputBytes {
		return 0, 0, ErrValidation(fmt.Sprintf("max_output_bytes must be between 1024 and %d", AppForkExecMaxOutputBytes))
	}
	return timeoutSeconds, maxOutput, nil
}

// AppForkExecStatus is queued, running, succeeded, failed or timed_out.
type AppForkExecStatus string

// AppForkExecResponse is one fork command and, once finished, its result.
// Stdout and stderr are the tail of the output, at most max_output_bytes
// together.
type AppForkExecResponse struct {
	ID              string            `json:"id"`
	ForkID          string            `json:"fork_id"`
	Command         []string          `json:"command"`
	Shell           bool              `json:"shell,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	MaxOutputBytes  int               `json:"max_output_bytes"`
	Status          AppForkExecStatus `json:"status"`
	ExitCode        *int              `json:"exit_code,omitempty"`
	OutputTruncated bool              `json:"output_truncated"`
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
	Failure         *AppForkFailure   `json:"failure,omitempty"`
	RequestedBy     string            `json:"requested_by"`
	CreatedAt       string            `json:"created_at"`
	StartedAt       *string           `json:"started_at,omitempty"`
	FinishedAt      *string           `json:"finished_at,omitempty"`
}

// AppForkExecListResponse is GET /v1/apps/{slug}/forks/{id}/execs.
type AppForkExecListResponse struct {
	Items []AppForkExecResponse `json:"items"`
}

// CodeAppForkExecRefused: the fork is not running, or already has the
// maximum number of commands pending.
const CodeAppForkExecRefused = "app_fork_exec_refused"

// ErrAppForkExecRefused is returned when a fork cannot take a command now.
func ErrAppForkExecRefused() *Problem {
	return NewProblem(http.StatusConflict, CodeAppForkExecRefused,
		"Command not accepted",
		fmt.Sprintf("the fork is not running, or already has %d commands pending", AppForkExecMaxPending)).
		WithDocs(docsBase + "/forks#run-commands")
}

// CreateAppForkExec queues a command inside a running fork. The key needs
// secrets:read as well as deploy:write.
func (c *Client) CreateAppForkExec(ctx context.Context, slug, forkID string, req CreateAppForkExecRequest) (AppForkExecResponse, error) {
	var out AppForkExecResponse
	return out, c.do(ctx, http.MethodPost, appForkExecsPath(slug, forkID), req, &out)
}

// GetAppForkExec returns one fork command and its result.
func (c *Client) GetAppForkExec(ctx context.Context, slug, forkID, execID string) (AppForkExecResponse, error) {
	var out AppForkExecResponse
	return out, c.do(ctx, http.MethodGet, appForkExecsPath(slug, forkID)+"/"+url.PathEscape(execID), nil, &out)
}

// ListAppForkExecs returns a fork's commands, newest first.
func (c *Client) ListAppForkExecs(ctx context.Context, slug, forkID string, limit int) (AppForkExecListResponse, error) {
	path := appForkExecsPath(slug, forkID)
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out AppForkExecListResponse
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func appForkExecsPath(slug, forkID string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/forks/" + url.PathEscape(forkID) + "/execs"
}
