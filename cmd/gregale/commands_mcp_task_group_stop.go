package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskGroupStopEntry struct {
	ReceiptIndex int                    `json:"receiptIndex"`
	Outcome      string                 `json:"outcome"`
	TaskID       string                 `json:"taskId,omitempty"`
	Task         *mcpTaskStatusMetadata `json:"task,omitempty"`
}

func runMCPTaskGroupStop(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root) int {
	if err := recordMCPTaskGroupStop(dir, manifest); err != nil {
		return printErr("MCP Task group stop", err)
	}
	entries := make([]mcpTaskGroupStopEntry, len(manifest.Members))
	for i := range entries {
		entries[i] = stopMCPTaskGroupMember(ctx, client, manifest, dir, i)
	}
	return jsonOut(writeJSON(entries))
}

func stopMCPTaskGroupMember(ctx context.Context, client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, index int) mcpTaskGroupStopEntry {
	entry := mcpTaskGroupStopEntry{ReceiptIndex: index}
	receipt, err := manifest.readMember(dir, index)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		entry.Outcome = "not_submitted"
	case err != nil:
		entry.Outcome = "invalid_receipt"
	default:
		entry = stopMCPTaskGroupReference(ctx, client, receipt, entry)
	}
	return entry
}

func stopMCPTaskGroupReference(ctx context.Context, client *mcphosting.Client, receipt mcpTaskGroupReference, entry mcpTaskGroupStopEntry) mcpTaskGroupStopEntry {
	task, exchange, err := readMCPTaskGroupReference(ctx, client, receipt)
	if err != nil {
		entry.Outcome = "unavailable"
		if receipt.TaskID == "" && taskGroupAdmissionUnconfirmed(exchange.HTTPStatus, err) {
			entry.Outcome = "admission_unconfirmed"
		}
		return entry
	}
	if mcpTaskTerminal(task.Status) {
		entry.Outcome = "terminal"
		snapshot := mcpTaskSnapshot(task)
		entry.Task = &snapshot
		return entry
	}
	_, err = client.CancelTask(ctx, task.TaskID)
	if err == nil {
		entry.Outcome, entry.TaskID = "requested", task.TaskID
		return entry
	}
	var unconfirmed *mcphosting.TaskCancellationUnconfirmedError
	var rpc *mcphosting.RPCError
	if errors.As(err, &unconfirmed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &rpc) && rpc.Code == -32603) {
		return reconcileMCPTaskGroupStop(ctx, client, receipt, task.TaskID, entry)
	}
	entry.Outcome = "unavailable"
	return entry
}

func taskGroupAdmissionUnconfirmed(status int, err error) bool {
	return status == http.StatusNotFound || mcphosting.IsTaskSubmissionNotFound(err) ||
		status == 0 && isTransportError(err)
}

func reconcileMCPTaskGroupStop(ctx context.Context, client *mcphosting.Client, receipt mcpTaskGroupReference, taskID string, entry mcpTaskGroupStopEntry) mcpTaskGroupStopEntry {
	entry.Outcome, entry.TaskID = "unconfirmed", taskID
	if ctx.Err() != nil {
		return entry
	}
	task, _, err := readMCPTaskGroupReference(ctx, client, receipt)
	if err != nil || task.TaskID != taskID {
		return entry
	}
	snapshot := mcpTaskSnapshot(task)
	if mcpTaskTerminal(task.Status) {
		entry.Outcome, entry.TaskID = "terminal", ""
	} else {
		snapshot.NextAction = "retry_status"
	}
	entry.Task = &snapshot
	return entry
}
