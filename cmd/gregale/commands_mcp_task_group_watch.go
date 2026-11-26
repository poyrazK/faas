package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpTaskGroupWatchEntry struct {
	receipt  mcpTaskGroupReference
	current  mcpTaskStatusMetadata
	updateID string
	pending  bool
}

type mcpTaskGroupWatchEvent struct {
	mcpTaskReceiptWatchEvent
	UpdateID string `json:"updateId,omitempty"`
}

type mcpTaskGroupWatcher struct {
	dir     *os.Root
	groupID string
	resume  bool
	client  *mcphosting.Client
	entries []mcpTaskGroupWatchEntry
	output  *json.Encoder
}

func runMCPTaskGroupWatch(client *mcphosting.Client, manifest mcpTaskGroupManifest, dir *os.Root, interval, timeout time.Duration, resume bool) int {
	interrupted, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, timeout)
	defer cancel()
	watcher := mcpTaskGroupWatcher{
		dir: dir, groupID: manifest.ID, resume: resume, client: client,
		entries: make([]mcpTaskGroupWatchEntry, len(manifest.Members)), output: json.NewEncoder(osStdout),
	}
	err := watcher.run(ctx, manifest, interval)
	switch {
	case err == nil || errors.Is(err, context.Canceled) && interrupted.Err() != nil:
		return 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded):
		return printErr("MCP Task group watch timed out", &exitErr{code: 124, msg: "--timeout elapsed while watching the Task group"})
	default:
		return printErr("MCP Task group watch", err)
	}
}

func (w *mcpTaskGroupWatcher) run(ctx context.Context, manifest mcpTaskGroupManifest, interval time.Duration) error {
	for i := range w.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		receipt, err := manifest.readMember(w.dir, i)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			err = w.report(i, mcpTaskReceiptStatusResult{Outcome: "not_submitted"}, "")
		case err != nil:
			err = w.report(i, mcpTaskReceiptStatusResult{Outcome: "invalid_receipt"}, "")
		default:
			w.entries[i] = mcpTaskGroupWatchEntry{receipt: receipt, pending: true}
			err = w.update(ctx, i)
		}
		if err != nil {
			return err
		}
	}
	return watchMCPTaskBatch(ctx, w.client, interval, max(30*time.Second, interval), w)
}

func (w *mcpTaskGroupWatcher) hasPendingTasks() bool {
	return slices.ContainsFunc(w.entries, func(entry mcpTaskGroupWatchEntry) bool { return entry.pending })
}

func (w *mcpTaskGroupWatcher) workingTasks(interval time.Duration) ([]mcphosting.Task, time.Duration) {
	tasks := make([]mcphosting.Task, 0, len(w.entries))
	for _, entry := range w.entries {
		if !entry.pending {
			continue
		}
		pollMS := min(entry.current.PollIntervalMS, time.Duration(math.MaxInt64).Milliseconds())
		interval = max(interval, time.Duration(pollMS)*time.Millisecond)
		if entry.current.Status == "working" {
			tasks = append(tasks, mcphosting.Task{
				TaskID: entry.current.TaskID, Status: entry.current.Status, LastUpdatedAt: entry.current.LastUpdatedAt,
			})
		}
	}
	return tasks, interval
}

func (w *mcpTaskGroupWatcher) refresh(ctx context.Context, taskID string) error {
	for i, entry := range w.entries {
		if entry.pending && (taskID == "" || taskID == entry.current.TaskID) {
			if err := w.update(ctx, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *mcpTaskGroupWatcher) update(ctx context.Context, index int) error {
	entry := &w.entries[index]
	task, exchange, err := readMCPTaskGroupReference(ctx, w.client, entry.receipt)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && entry.current.TaskID != "" && task.TaskID != entry.current.TaskID {
		err = errors.New("task receipt lookup returned a different Task")
	}
	result, _ := mcpTaskReceiptLookupOutcome(task, exchange, err)
	if result.Outcome == "retry" && entry.current.Status != "" {
		// Keep the last observation and the pinned ID through transient
		// failures. Recovering the same status should not emit it twice.
		return nil
	}
	entry.pending = result.Outcome == "retry" || (result.Outcome == "available" && task.Status == "working")
	if result.Task != nil {
		entry.current = *result.Task
	}
	if result.Outcome != "available" {
		return w.report(index, result, "")
	}
	id := mcpTaskGroupUpdateID(w.groupID, index, task)
	if id == entry.updateID {
		return nil
	}
	entry.updateID = id
	if w.resume {
		acknowledged, err := mcpTaskGroupUpdateAcknowledged(w.dir, id)
		if err != nil || acknowledged {
			return err
		}
	}
	return w.report(index, result, id)
}

func (w *mcpTaskGroupWatcher) report(index int, status mcpTaskReceiptStatusResult, updateID string) error {
	return w.output.Encode(mcpTaskGroupWatchEvent{
		mcpTaskReceiptWatchEvent: mcpTaskReceiptWatchEvent{ReceiptIndex: index, mcpTaskReceiptStatusResult: status},
		UpdateID:                 updateID,
	})
}
