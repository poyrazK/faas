package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func collectJobTaskPages(client *api.Client, name, runID string, limit, startingOffset int, failedOnly bool) ([]api.JobTaskResponse, error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return collectJobTaskPagesContext(ctx, client, name, runID, limit, startingOffset, failedOnly)
}

func collectJobTaskPagesContext(ctx context.Context, client *api.Client, name, runID string, limit, startingOffset int, failedOnly bool) ([]api.JobTaskResponse, error) {
	seen := map[int]bool{}
	tasks, _, err := collectOffsetPages(ctx, startingOffset, true, func(ctx context.Context, offset int) ([]api.JobTaskResponse, int, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		page, err := client.ListJobRunTasksPage(readCtx, name, runID, limit, offset)
		if err != nil {
			return nil, -1, err
		}
		if page.NextOffset < -1 || (page.NextOffset > 0 && page.NextOffset <= offset) {
			return nil, -1, fmt.Errorf("task pagination offset did not advance")
		}
		if len(page.Tasks) == 0 && (page.Total > offset || page.NextOffset > offset) {
			return nil, -1, fmt.Errorf("task history returned an empty page before its reported end")
		}
		filtered := make([]api.JobTaskResponse, 0)
		for _, task := range page.Tasks {
			if task.RunID != runID || task.TaskIndex < 0 || seen[task.TaskIndex] {
				return nil, -1, fmt.Errorf("task page contains another run, an invalid index, or a repeated task")
			}
			seen[task.TaskIndex] = true
			if !failedOnly || task.Status == "failed" || task.Status == "timeout" || task.Status == "oom" {
				filtered = append(filtered, task)
			}
		}
		next := -1
		if page.NextOffset > offset {
			next = page.NextOffset
		} else if end := offset + len(page.Tasks); page.Total > end && end > offset {
			next = end
		}
		return filtered, next, nil
	})
	return tasks, err
}
