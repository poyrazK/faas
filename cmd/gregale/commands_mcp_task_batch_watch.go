package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskReceiptWatchEvent struct {
	ReceiptIndex int `json:"receiptIndex"`
	mcpTaskReceiptStatusResult
}

type mcpTaskReceiptWatchEntry struct {
	key  string
	task mcphosting.Task
}

type mcpTaskReceiptBatchWatcher struct {
	client   *mcphosting.Client
	entries  []mcpTaskReceiptWatchEntry
	output   *json.Encoder
	working  int
	exitCode int
}

func runMCPTaskReceiptBatchWatch(ctx context.Context, client *mcphosting.Client, receipts []string, interval time.Duration) int {
	watch := mcpTaskReceiptBatchWatcher{
		client: client, entries: make([]mcpTaskReceiptWatchEntry, len(receipts)), output: json.NewEncoder(osStdout),
	}
	if err := watch.run(ctx, receipts, interval); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return 0
		}
		return printErr("MCP task watch", err)
	}
	return watch.exitCode
}

func (w *mcpTaskReceiptBatchWatcher) run(ctx context.Context, receipts []string, interval time.Duration) error {
	for i, receipt := range receipts {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := readMCPTaskReceipt(receipt, w.client.Endpoint)
		if err != nil {
			if err := w.report(i, mcpTaskReceiptStatusResult{Outcome: "invalid_receipt"}, 1); err != nil {
				return err
			}
			continue
		}
		w.entries[i].key = key
		if err := w.update(ctx, i); err != nil {
			return err
		}
	}

	return watchMCPTaskBatch(ctx, w.client, interval, interval, w)
}

func (w *mcpTaskReceiptBatchWatcher) hasPendingTasks() bool {
	return w.working > 0
}

type mcpTaskBatchWatcher interface {
	hasPendingTasks() bool
	workingTasks(time.Duration) ([]mcphosting.Task, time.Duration)
	refresh(context.Context, string) error
}

// A subscription wakes the watcher early. Every interruption or interval
// boundary also reconciles the active Tasks using authenticated reads.
func watchMCPTaskBatch(ctx context.Context, client *mcphosting.Client, pollInterval, streamWindow time.Duration, watcher mcpTaskBatchWatcher) error {
	streaming := true
	failures := 0
	reconnectDelay := mcpWatchReconnectBase
	for watcher.hasPendingTasks() {
		tasks, pollDelay := watcher.workingTasks(pollInterval)
		if streaming && len(tasks) > 0 && len(tasks) <= mcphosting.MaxTaskSubscriptionIDs {
			streamCtx, cancel := context.WithTimeout(ctx, streamWindow)
			var updateErr error
			streamErr := client.ListenTaskChanges(streamCtx, tasks, func(taskID string) (bool, error) {
				updateErr = watcher.refresh(ctx, taskID)
				return !watcher.hasPendingTasks(), updateErr
			})
			expired := errors.Is(streamCtx.Err(), context.DeadlineExceeded)
			cancel()
			if updateErr != nil {
				return updateErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch {
			case streamErr == nil || expired:
				failures = 0
				reconnectDelay = mcpWatchReconnectBase
			case errors.Is(streamErr, mcphosting.ErrTaskSubscriptionClosed), errors.Is(streamErr, mcphosting.ErrTaskSubscriptionInterrupted):
				failures++
				streaming = failures < mcpWatchMaxReconnects
			default:
				// Fresh lookups isolate lost access or a refused subscription
				// to the affected receipts, then polling follows the rest.
				streaming = false
			}
		} else if err := sleepContext(ctx, pollDelay); err != nil {
			return err
		}

		if err := watcher.refresh(ctx, ""); err != nil {
			return err
		}
		if watcher.hasPendingTasks() && streaming && failures > 0 {
			if err := sleepContext(ctx, reconnectDelay); err != nil {
				return err
			}
			reconnectDelay = min(reconnectDelay*2, mcpWatchReconnectMax)
		}
	}
	return nil
}

func (w *mcpTaskReceiptBatchWatcher) workingTasks(interval time.Duration) ([]mcphosting.Task, time.Duration) {
	tasks := make([]mcphosting.Task, 0, w.working)
	seen := make(map[string]bool, w.working)
	for _, entry := range w.entries {
		if entry.task.Status == "working" && !seen[entry.task.TaskID] {
			seen[entry.task.TaskID] = true
			tasks = append(tasks, entry.task)
			interval = max(interval, entry.task.PollDelay())
		}
	}
	return tasks, interval
}

// An empty task ID refreshes all receipts that are still being watched.
func (w *mcpTaskReceiptBatchWatcher) refresh(ctx context.Context, taskID string) error {
	for i, entry := range w.entries {
		if entry.task.Status == "working" && (taskID == "" || entry.task.TaskID == taskID) {
			if err := w.update(ctx, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *mcpTaskReceiptBatchWatcher) update(ctx context.Context, index int) error {
	entry := &w.entries[index]
	previous := entry.task
	next, exchange, err := w.client.GetTaskBySubmissionKey(ctx, entry.key)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && previous.TaskID != "" && next.TaskID != previous.TaskID {
		err = errors.New("task receipt lookup returned a different Task")
	}
	result, code := mcpTaskReceiptLookupOutcome(next, exchange, err)
	if err != nil {
		next = mcphosting.Task{}
	}
	entry.task = next
	if previous.Status == "working" && next.Status != "working" {
		w.working--
	}
	if previous.Status == "" && next.Status == "working" {
		w.working++
	}
	if code == 0 && *result.Task == mcpTaskSnapshot(previous) {
		return nil
	}
	return w.report(index, result, code)
}

func (w *mcpTaskReceiptBatchWatcher) report(index int, result mcpTaskReceiptStatusResult, code int) error {
	w.exitCode = max(w.exitCode, code)
	return w.output.Encode(mcpTaskReceiptWatchEvent{ReceiptIndex: index, mcpTaskReceiptStatusResult: result})
}
