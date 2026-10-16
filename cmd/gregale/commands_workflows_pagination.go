package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func collectWorkflowRunPages(client *api.Client, app string, opts api.WorkflowRunListOptions, all bool) ([]api.WorkflowRunResponse, int, error) {
	ctx := context.Background()
	if all {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	items, next, err := collectOffsetPages(ctx, opts.Offset, all, func(ctx context.Context, offset int) ([]api.WorkflowRunResponse, int, error) {
		request := opts
		request.Offset = offset
		if all {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
		}
		page, err := client.ListWorkflowRunsWithOptions(ctx, app, request)
		if err != nil {
			return nil, -1, err
		}
		next := -1
		end := offset + len(page.Runs)
		if end < offset {
			return nil, -1, fmt.Errorf("workflow history offset overflow; use a smaller --offset")
		}
		if len(page.Runs) > 0 && page.Total > end {
			next = end
		}
		if all && len(page.Runs) == 0 && page.Total > offset {
			return nil, -1, fmt.Errorf("workflow history returned an empty page before its reported end; rerun with a narrower date range")
		}
		return page.Runs, next, nil
	})
	if err != nil || !all {
		return items, next, err
	}
	// Offset history can shift while new runs arrive. Avoid repeated rows, but
	// do not claim this produces an atomic snapshot of changing history.
	unique := make([]api.WorkflowRunResponse, 0, len(items))
	seen := map[string]bool{}
	for _, run := range items {
		if run.ID == "" || !seen[run.ID] {
			unique = append(unique, run)
			seen[run.ID] = true
		}
	}
	return unique, next, nil
}

func workflowListNextCommand(app string, opts api.WorkflowRunListOptions) string {
	parts := []string{recoveryCLI(), "workflows list --app", recoveryArgument(app), "--limit", strconv.Itoa(opts.Limit), "--offset", strconv.Itoa(opts.Offset)}
	for _, pair := range [][2]string{{"status", opts.Status}, {"workflow-name", opts.WorkflowName}} {
		if pair[1] != "" {
			parts = append(parts, "--"+pair[0], recoveryArgument(pair[1]))
		}
	}
	if opts.CreatedAfter != nil {
		parts = append(parts, "--created-after", recoveryArgument(opts.CreatedAfter.UTC().Format(time.RFC3339Nano)))
	}
	if opts.CreatedBefore != nil {
		parts = append(parts, "--created-before", recoveryArgument(opts.CreatedBefore.UTC().Format(time.RFC3339Nano)))
	}
	return strings.Join(parts, " ")
}
