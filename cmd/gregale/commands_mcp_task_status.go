package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskStatusMetadata struct {
	TaskID         string `json:"taskId,omitempty"`
	Status         string `json:"status"`
	LastUpdatedAt  string `json:"lastUpdatedAt,omitempty"`
	PollIntervalMS int64  `json:"pollIntervalMs,omitempty"`
	NextAction     string `json:"nextAction"`
}

func runMCPTaskMetadataStatus(ctx context.Context, client *mcphosting.Client, taskID, submissionKey string) int {
	var task mcphosting.Task
	var exchange mcphosting.Exchange
	var err error
	if submissionKey == "" {
		task, exchange, err = client.GetTask(ctx, taskID)
	} else {
		task, exchange, err = client.GetTaskBySubmissionKey(ctx, submissionKey)
	}
	if err != nil {
		return mcpTaskMetadataReadFailure(taskID, exchange.HTTPStatus, err)
	}
	return jsonOut(writeJSON(mcpTaskSnapshot(task)))
}

func mcpTaskSnapshot(task mcphosting.Task) mcpTaskStatusMetadata {
	status := mcpTaskStatusMetadata{
		TaskID: task.TaskID, Status: task.Status, LastUpdatedAt: task.LastUpdatedAt, NextAction: "none",
	}
	switch task.Status {
	case "working":
		status.NextAction = "poll_status"
		status.PollIntervalMS = task.PollDelay().Milliseconds()
	case "input_required":
		status.NextAction = "provide_input"
	case "completed":
		status.NextAction = "read_result"
	}
	return status
}

func mcpTaskInputUpdateStatus(update *mcphosting.TaskInputUpdateUnconfirmedError) int {
	if update.Task.Status == "" {
		return mcpTaskRetryStatus(update.Task.TaskID, update)
	}
	if code := jsonOut(writeJSON(mcpTaskSnapshot(update.Task))); code != 0 {
		return code
	}
	return exitCodeForStatus(http.StatusServiceUnavailable)
}

func mcpTaskMetadataReadFailure(taskID string, httpStatus int, err error) int {
	if errors.Is(err, context.Canceled) {
		return printErr("MCP task status", context.Canceled)
	}
	// The discovery exchange is inside GetTask and can fail before a task
	// request. Its sanitized API error still carries the HTTP status.
	var upstream *api.APIError
	if errors.As(err, &upstream) {
		httpStatus = upstream.Problem.Status
	}
	if httpStatus == http.StatusRequestTimeout || httpStatus == http.StatusTooManyRequests ||
		httpStatus >= http.StatusInternalServerError || httpStatus == 0 && isTransportError(err) {
		return mcpTaskRetryStatus(taskID, err)
	}
	if upstream != nil {
		// The MCP transport constructs this from the HTTP status and keeps
		// response bodies and authentication challenges out of the error.
		return printErr("MCP task status", upstream)
	}
	return printErr("MCP task status", errors.New("the server did not confirm the requested Task status"))
}

func mcpTaskRetryStatus(taskID string, err error) int {
	status := mcpTaskStatusMetadata{TaskID: taskID, Status: "unknown", NextAction: "retry_status"}
	var upstream *api.APIError
	if errors.As(err, &upstream) && upstream.Problem.RetryAfterSeconds != nil {
		seconds := *upstream.Problem.RetryAfterSeconds
		if seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second/time.Millisecond) {
			status.PollIntervalMS = seconds * int64(time.Second/time.Millisecond)
		}
	}
	if code := jsonOut(writeJSON(status)); code != 0 {
		return code
	}
	return exitCodeForStatus(http.StatusServiceUnavailable)
}
