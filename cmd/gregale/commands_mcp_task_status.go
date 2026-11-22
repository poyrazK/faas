package main

import (
	"context"
	"encoding/json"
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

type mcpTaskReceiptStatusResult struct {
	Outcome string                 `json:"outcome"`
	Task    *mcpTaskStatusMetadata `json:"task,omitempty"`
}

func runMCPTaskReceiptBatchStatus(ctx context.Context, client *mcphosting.Client, receipts []string) int {
	results := make([]mcpTaskReceiptStatusResult, len(receipts))
	exit := 0
	for i, receipt := range receipts {
		result, code := readMCPTaskReceiptStatus(ctx, client, receipt)
		results[i] = result
		exit = max(exit, code)
	}
	if code := jsonOut(writeJSON(results)); code != 0 {
		return code
	}
	return exit
}

func readMCPTaskReceiptStatus(ctx context.Context, client *mcphosting.Client, receipt string) (mcpTaskReceiptStatusResult, int) {
	retry := func(err error) (mcpTaskReceiptStatusResult, int) {
		status := mcpTaskRetrySnapshot("", err)
		return mcpTaskReceiptStatusResult{Outcome: "retry", Task: &status}, exitCodeForStatus(http.StatusServiceUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return retry(err)
	}
	key, err := readMCPTaskReceipt(receipt, client.Endpoint)
	if err != nil {
		return mcpTaskReceiptStatusResult{Outcome: "invalid_receipt"}, 1
	}
	task, exchange, err := client.GetTaskBySubmissionKey(ctx, key)
	if err != nil {
		if mcpTaskMetadataReadRetryable(exchange.HTTPStatus, err) {
			return retry(err)
		}
		return mcpTaskReceiptStatusResult{Outcome: "unavailable"}, 1
	}
	status := mcpTaskSnapshot(task)
	return mcpTaskReceiptStatusResult{Outcome: "available", Task: &status}, 0
}

func runMCPTaskReceiptWatch(ctx context.Context, client *mcphosting.Client, receipt string, interval time.Duration) int {
	if client.Token == "" {
		return printErr("MCP Task receipt", errors.New("--task-receipt requires a client credential from --token-env"))
	}
	key, err := readMCPTaskReceipt(receipt, client.Endpoint)
	if err != nil {
		return printErr("MCP Task receipt", err)
	}
	output := json.NewEncoder(osStdout)
	if err := client.WatchTaskBySubmissionKey(ctx, key, interval, func(task mcphosting.Task) error {
		return output.Encode(mcpTaskSnapshot(task))
	}); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return 0
		}
		return printErr("MCP task watch", err)
	}
	return 0
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

func runMCPTaskReceiptResult(ctx context.Context, client *mcphosting.Client, submissionKey string) int {
	task, exchange, err := client.GetTaskBySubmissionKey(ctx, submissionKey)
	if err != nil {
		return mcpTaskMetadataReadFailure("", exchange.HTTPStatus, err)
	}
	if task.Status != "completed" {
		return jsonOut(writeJSON(mcpTaskSnapshot(task)))
	}
	result, exchange, err := client.GetTask(ctx, task.TaskID)
	if err != nil {
		return mcpTaskMetadataReadFailure(task.TaskID, exchange.HTTPStatus, err)
	}
	if result.Status != "completed" {
		return jsonOut(writeJSON(mcpTaskSnapshot(result)))
	}
	return jsonOut(writeJSON(result))
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

func runMCPTaskReceiptCancel(ctx context.Context, client *mcphosting.Client, submissionKey string) int {
	task, exchange, err := client.GetTaskBySubmissionKey(ctx, submissionKey)
	if err != nil {
		return mcpTaskMetadataReadFailure("", exchange.HTTPStatus, err)
	}
	if mcpTaskTerminal(task.Status) {
		return jsonOut(writeJSON(mcpTaskSnapshot(task)))
	}
	if _, err := client.CancelTask(ctx, task.TaskID); err != nil {
		var unconfirmed *mcphosting.TaskCancellationUnconfirmedError
		if errors.As(err, &unconfirmed) {
			return mcpTaskReconcileCancellation(ctx, client, submissionKey, task.TaskID)
		}
		return printErr("MCP task cancel", err)
	}
	return mcpTaskCancellationRequested(task.TaskID)
}

func mcpTaskReconcileCancellation(ctx context.Context, client *mcphosting.Client, submissionKey, taskID string) int {
	if err := ctx.Err(); err != nil {
		return mcpTaskMetadataReadFailure(taskID, 0, err)
	}
	current, exchange, err := client.GetTaskBySubmissionKey(ctx, submissionKey)
	if err != nil {
		return mcpTaskMetadataReadFailure(taskID, exchange.HTTPStatus, err)
	}
	if current.TaskID != taskID {
		return printErr("MCP task cancel", errors.New("task receipt lookup returned a different Task"))
	}
	snapshot := mcpTaskSnapshot(current)
	if mcpTaskTerminal(current.Status) {
		return jsonOut(writeJSON(snapshot))
	}
	snapshot.NextAction = "retry_status"
	if code := jsonOut(writeJSON(snapshot)); code != 0 {
		return code
	}
	return exitCodeForStatus(http.StatusServiceUnavailable)
}

func mcpTaskTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

func mcpTaskCancellationRequested(taskID string) int {
	return jsonOut(writeJSON(map[string]any{"task_id": taskID, "cancellation_requested": true}))
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
	if mcpTaskMetadataReadRetryable(httpStatus, err) {
		return mcpTaskRetryStatus(taskID, err)
	}
	var upstream *api.APIError
	if errors.As(err, &upstream) {
		// The MCP transport constructs this from the HTTP status and keeps
		// response bodies and authentication challenges out of the error.
		return printErr("MCP task status", upstream)
	}
	return printErr("MCP task status", errors.New("the server did not confirm the requested Task status"))
}

func mcpTaskMetadataReadRetryable(httpStatus int, err error) bool {
	// Discovery can fail before a Task request and return its HTTP status
	// through the API error.
	var upstream *api.APIError
	if errors.As(err, &upstream) {
		httpStatus = upstream.Problem.Status
	}
	return httpStatus == http.StatusRequestTimeout || httpStatus == http.StatusTooManyRequests ||
		httpStatus >= http.StatusInternalServerError || httpStatus == 0 && isTransportError(err)
}

func mcpTaskRetrySnapshot(taskID string, err error) mcpTaskStatusMetadata {
	status := mcpTaskStatusMetadata{TaskID: taskID, Status: "unknown", NextAction: "retry_status"}
	var upstream *api.APIError
	if errors.As(err, &upstream) && upstream.Problem.RetryAfterSeconds != nil {
		seconds := *upstream.Problem.RetryAfterSeconds
		if seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second/time.Millisecond) {
			status.PollIntervalMS = seconds * int64(time.Second/time.Millisecond)
		}
	}
	return status
}

func mcpTaskRetryStatus(taskID string, err error) int {
	if code := jsonOut(writeJSON(mcpTaskRetrySnapshot(taskID, err))); code != 0 {
		return code
	}
	return exitCodeForStatus(http.StatusServiceUnavailable)
}
