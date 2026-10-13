package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func splitJobFailedLogsFlag(args []string) ([]string, bool, error) {
	remaining := []string{}
	found, enabled := false, false
	for _, arg := range args {
		if arg == "--failed-only" || arg == "-failed-only" || strings.HasPrefix(arg, "--failed-only=") || strings.HasPrefix(arg, "-failed-only=") {
			if found {
				return nil, false, fmt.Errorf("--failed-only may be supplied only once")
			}
			found = true
			enabled = true
			if strings.Contains(arg, "=") {
				value, err := strconv.ParseBool(arg[strings.IndexByte(arg, '=')+1:])
				if err != nil {
					return nil, false, fmt.Errorf("--failed-only requires a boolean value")
				}
				enabled = value
			}
		} else {
			remaining = append(remaining, arg)
		}
	}
	return remaining, enabled, nil
}

func cmdJobsFailedLogs(args []string) int {
	positionals, maxBytes, _, ok := parseJobsLogsArgs(args)
	if !ok || len(positionals) > 2 {
		return printErr("Invalid failed logs arguments", fmt.Errorf("use jobs logs [JOB] [RUN_ID|latest] --failed-only [--max-bytes N]; --task, positional task indexes, and --attempt cannot be combined with --failed-only"))
	}
	name, ref := "", "latest"
	if len(positionals) > 0 {
		name = positionals[0]
	}
	if len(positionals) == 2 {
		ref = positionals[1]
	}
	if name == "" {
		d, path, err := readJobDefaults()
		if err != nil {
			return printErr("Could not read Job defaults", err)
		}
		name = d.Job
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, name)
		}
	}
	if !jobSlugPattern.MatchString(name) || (ref != "latest" && !jobRunIDPattern.MatchString(ref)) {
		return printErr("Invalid failed logs target", fmt.Errorf("provide a valid Job name and a run UUID or latest"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 5*time.Minute)
	defer cancel()
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	job, err := client.GetJob(readCtx, name)
	if err != nil {
		readCancel()
		return printErr("Could not read Job", err)
	}
	if job.Name != name || job.ID == "" || job.AccountID == "" {
		readCancel()
		return printErr("Invalid Job", fmt.Errorf("server did not identify the requested Job and account"))
	}
	id, err := resolveJobRunReadArg(readCtx, client, name, ref)
	if err != nil {
		readCancel()
		return printErr("Could not resolve Job run", err)
	}
	run, err := client.GetJobRun(readCtx, name, id)
	readCancel()
	if err != nil {
		return printErr("Could not read Job run", err)
	}
	if run.ID != id || run.JobID != job.ID || run.AccountID != job.AccountID {
		return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
	}
	tasks, err := collectJobTaskPagesContext(ctx, client, name, id, 200, 0, true)
	if err != nil {
		return printErr("Could not inspect failed tasks", err)
	}
	summary := failedJobLogsSummary{Type: "summary", Job: name, RunID: id, Found: len(tasks), NotAttempted: len(tasks), Unavailable: []failedJobLogUnavailable{}}
	if len(tasks) == 0 {
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "No failed, timed-out, or out-of-memory tasks recorded for Job %s run %s.\n", name, id)
		}
		return renderFailedJobLogsSummary(summary)
	}
	var firstFailure error
	finish := func(stopped error) int {
		if code := renderFailedJobLogsSummary(summary); code != 0 {
			return code
		}
		if stopped != nil {
			if errors.Is(stopped, context.DeadlineExceeded) {
				return printErr("Failed task logs deadline exceeded", &exitErr{msg: "overall log collection deadline exceeded; remaining tasks were not attempted", code: 124})
			}
			return printErr("Failed task logs cancelled", stopped)
		}
		if firstFailure != nil {
			return printErr("Failed task logs incomplete", firstFailure)
		}
		return 0
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
		logs, err := client.GetJobTaskLogsWithMaxBytes(readCtx, name, id, task.TaskIndex, maxBytes)
		readCancel()
		if err != nil {
			summary.NotAttempted--
			retry := fmt.Sprintf("%s jobs logs %s %s --task %d --max-bytes %d", recoveryCLI(), recoveryArgument(name), recoveryArgument(id), task.TaskIndex, maxBytes)
			summary.Unavailable = append(summary.Unavailable, failedJobLogUnavailable{TaskIndex: task.TaskIndex, RetryCommand: retry, Error: redactErrorText(err.Error())})
			if firstFailure == nil {
				var apiErr *APIError
				if errors.As(err, &apiErr) {
					scoped := *apiErr
					scoped.Problem.Detail = fmt.Sprintf("task %d in run %s: %s; unavailable logs and retry commands are listed in the summary", task.TaskIndex, id, apiErr.Problem.Detail)
					firstFailure = &scoped
				} else {
					firstFailure = fmt.Errorf("task %d in run %s: %w; unavailable logs and retry commands are listed in the summary", task.TaskIndex, id, err)
				}
			}
			if stopped := ctx.Err(); stopped != nil {
				return finish(stopped)
			}
			continue
		}
		// Enforce the requested tail bound even if a server returns excess content.
		if len(logs.LogContent) > maxBytes {
			start := len(logs.LogContent) - maxBytes
			for start < len(logs.LogContent) && !utf8.RuneStart(logs.LogContent[start]) {
				start++
			}
			logs.LogContent = logs.LogContent[start:]
			logs.Truncated = true
		}
		summary.Read++
		summary.NotAttempted--
		if logs.LogContent == "" {
			summary.Empty++
		}
		if logs.Truncated {
			summary.Truncated++
		}
		if jsonOutput {
			receipt := struct {
				api.JobTaskLogResponse
				Job               string `json:"job"`
				RunID             string `json:"run_id"`
				TaskIndex         int    `json:"task_index"`
				DiscoveredAttempt int    `json:"discovered_attempt"`
				DiscoveredStatus  string `json:"discovered_status"`
			}{logs, name, id, task.TaskIndex, task.Attempt, task.Status}
			if code := jsonOut(writeJSONSingle(receipt)); code != 0 {
				return code
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "\nJob %s; run %s; task %d; discovered attempt %d (%s); current log status %s\n", name, id, task.TaskIndex, task.Attempt, oneLine(task.Status), oneLine(logs.TaskStatus))
			renderJobTaskLogs(logs)
			if logs.LogContent != "" && !strings.HasSuffix(logs.LogContent, "\n") {
				_, _ = fmt.Fprintln(osStdout)
			}
		}
	}
	return finish(ctx.Err())
}

type failedJobLogUnavailable struct {
	TaskIndex    int    `json:"task_index"`
	RetryCommand string `json:"retry_command"`
	Error        string `json:"error,omitempty"`
}

type failedJobLogsSummary struct {
	Type         string                    `json:"type"`
	Job          string                    `json:"job"`
	RunID        string                    `json:"run_id"`
	Found        int                       `json:"found"`
	Read         int                       `json:"read"`
	Empty        int                       `json:"empty"`
	Truncated    int                       `json:"truncated"`
	NotAttempted int                       `json:"not_attempted"`
	Unavailable  []failedJobLogUnavailable `json:"unavailable"`
}

func renderFailedJobLogsSummary(summary failedJobLogsSummary) int {
	if jsonOutput {
		return jsonOut(writeJSONSingle(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "\nFailed-task log summary: %d found; %d logs read; %d empty; %d truncated; %d unavailable; %d not attempted.\n", summary.Found, summary.Read, summary.Empty, summary.Truncated, len(summary.Unavailable), summary.NotAttempted)
	for _, task := range summary.Unavailable {
		_, _ = fmt.Fprintf(osStdout, "Task %d logs unavailable: %s\nRetry reading task %d logs (POSIX shells):\n  %s\n", task.TaskIndex, oneLine(task.Error), task.TaskIndex, task.RetryCommand)
	}
	return 0
}
