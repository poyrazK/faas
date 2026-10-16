package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func splitJobLogAttempt(args []string) ([]string, int, error) {
	remaining := make([]string, 0, len(args))
	attempt := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var raw string
		if arg == "--attempt" || arg == "-attempt" {
			if i+1 == len(args) {
				return nil, 0, fmt.Errorf("--attempt requires a positive attempt number")
			}
			i++
			raw = args[i]
		} else if strings.HasPrefix(arg, "--attempt=") || strings.HasPrefix(arg, "-attempt=") {
			raw = arg[strings.IndexByte(arg, '=')+1:]
		} else {
			remaining = append(remaining, arg)
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || attempt != 0 {
			return nil, 0, fmt.Errorf("provide --attempt once with a positive attempt number")
		}
		attempt = n
	}
	return remaining, attempt, nil
}

func readRetainedJobAttemptLogs(ctx context.Context, client *api.Client, name, runID string, taskIndex, attempt, maxBytes int) int {
	if maxBytes == 0 {
		maxBytes = api.DefaultJobTaskLogMaxBytes
	}
	offset := 0
	seen := map[int]bool{}
	for pageNumber := 0; pageNumber < maxCLIListPages; pageNumber++ {
		page, err := client.ListJobTaskAttemptsPage(ctx, name, runID, taskIndex, 100, offset)
		if err != nil {
			return printErr("Could not read retained attempt", err)
		}
		var selected *api.JobTaskAttemptResponse
		for i := range page.Attempts {
			row := &page.Attempts[i]
			if row.RunID != runID || row.TaskIndex != taskIndex || row.Attempt < 1 || seen[row.Attempt] {
				return printErr("Invalid attempt history", fmt.Errorf("history returned another task, an invalid attempt, or a repeated attempt"))
			}
			seen[row.Attempt] = true
			if row.Attempt == attempt {
				selected = row
			}
		}
		if selected != nil {
			content, truncated := selected.LogContent, selected.LogTruncated
			if len(content) > maxBytes {
				start := len(content) - maxBytes
				for start < len(content) && !utf8.RuneStart(content[start]) {
					start++
				}
				content, truncated = content[start:], true
			}
			logs := api.JobTaskLogResponse{TaskStatus: selected.Status, LogContent: content, Truncated: truncated, MaxBytes: maxBytes}
			if jsonOutput {
				return jsonOut(writeJSONSingle(struct {
					api.JobTaskLogResponse
					RunID     string `json:"run_id"`
					TaskIndex int    `json:"task_index"`
					Attempt   int    `json:"attempt"`
				}{logs, runID, taskIndex, attempt}))
			}
			_, _ = fmt.Fprintf(osStderr, "Retained task output: Job=%s; run=%s; task=%d; attempt=%d; status=%s.\n", name, runID, taskIndex, attempt, oneLine(selected.Status))
			if truncated {
				_, _ = fmt.Fprintln(osStderr, "Output is truncated; retained history or the local byte limit may omit earlier output.")
			}
			return renderJobTaskLogs(logs)
		}
		if page.NextOffset == -1 || (page.NextOffset == 0 && offset == 0) {
			return printErr("Retained attempt not found", fmt.Errorf("attempt %d was not found in retained history; inspect %s jobs attempts %s %s %d; it may be active or no longer retained", attempt, recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), taskIndex))
		}
		if len(page.Attempts) == 0 || page.NextOffset <= offset {
			return printErr("Invalid attempt history", fmt.Errorf("attempt pagination did not advance"))
		}
		offset = page.NextOffset
	}
	return printErr("Could not read retained attempt", fmt.Errorf("attempt history exceeded %d pages", maxCLIListPages))
}
