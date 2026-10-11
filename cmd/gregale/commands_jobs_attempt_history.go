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

func collectJobAttemptHistory(client *api.Client, singleCtx context.Context, name, runID string, taskIndex, limit, offset int, all bool) (api.ListJobTaskAttemptsResponse, error) {
	if !all {
		return client.ListJobTaskAttemptsPage(singleCtx, name, runID, taskIndex, limit, offset)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var result api.ListJobTaskAttemptsResponse
	seen := map[int]bool{}
	items, next, err := collectOffsetPages(ctx, offset, true, func(ctx context.Context, current int) ([]api.JobTaskAttemptResponse, int, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		page, err := client.ListJobTaskAttemptsPage(readCtx, name, runID, taskIndex, limit, current)
		if err != nil {
			return nil, -1, err
		}
		if current == offset {
			result.Limit, result.Offset = page.Limit, offset
		}
		if len(page.Attempts) == 0 && page.NextOffset >= 0 {
			return nil, -1, fmt.Errorf("attempt history returned an empty page with a continuation")
		}
		for _, row := range page.Attempts {
			if row.RunID != runID || row.TaskIndex != taskIndex || row.Attempt < 1 || seen[row.Attempt] {
				return nil, -1, fmt.Errorf("attempt history returned another task, an invalid attempt, or a repeated attempt")
			}
			seen[row.Attempt] = true
		}
		return page.Attempts, page.NextOffset, nil
	})
	if err != nil {
		return api.ListJobTaskAttemptsResponse{}, err
	}
	result.Attempts, result.NextOffset = items, next
	return result, nil
}
