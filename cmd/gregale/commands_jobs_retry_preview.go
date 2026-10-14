package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func previewJobRetry(ctx context.Context, client *api.Client, name, runID string, index int, options jobRecoveryPreviewOptions) int {
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return printErr("Could not inspect retry Job", err)
	}
	if job.Name != name || !jobRunIDPattern.MatchString(job.ID) || job.AccountID == "" {
		return printErr("Invalid retry target", errors.New("job response did not identify the requested Job"))
	}
	run, task, err := readJobRetryTarget(ctx, client, job, runID, index)
	if err != nil {
		return printErr("Could not inspect retry target", err)
	}
	if run.Tasks <= index || run.RetryMax < 0 || run.RetryMax == math.MaxInt || task.Attempt < 0 {
		return printErr("Invalid retry target", errors.New("source task index, attempt, or retry budget is invalid"))
	}
	switch task.Status {
	case "queued", "claimed", "running", "succeeded", "failed", "timeout", "oom", "cancelled":
	default:
		return printErr("Invalid retry target", errors.New("source task has an unknown status"))
	}
	now := time.Now().UTC()
	blockers := []string{}
	if err := jobRetryEligibility(run, task, now); err != nil {
		blockers = append(blockers, err.Error())
	}
	remaining := max(0, run.RetryMax-task.Attempt+1)
	var nextSteps []jobRecoveryPreviewStep
	if len(blockers) > 0 {
		nextSteps = blockedJobRetryNextSteps(name, runID, task, options)
	}
	command, resultsDirectory, waitTimeout, pollInterval := "", "", "", ""
	if len(blockers) == 0 {
		args := []string{"jobs", "retry", name, runID, strconv.Itoa(index)}
		command = savedNameSuggestionCommand(append(args, jobRecoveryPreviewFollowArgs(options)...))
	}
	if options.CollectResults {
		resultsDirectory = filepath.Join(options.ResultsParent, runID)
	}
	if options.Wait {
		waitTimeout, pollInterval = options.WaitTimeout.String(), options.PollInterval.String()
	}
	report := struct {
		DryRun           bool                      `json:"dry_run"`
		Changed          bool                      `json:"changed"`
		Job              string                    `json:"job"`
		RunID            string                    `json:"run_id"`
		TaskIndex        int                       `json:"task_index"`
		TaskStatus       string                    `json:"task_status"`
		Attempt          int                       `json:"attempt"`
		RetryMax         int                       `json:"retry_max"`
		RemainingBudget  int                       `json:"remaining_retry_budget"`
		RetryDelay       string                    `json:"retry_delay"`
		ObservedAt       string                    `json:"observed_at"`
		ExecutionClass   string                    `json:"execution_class"`
		LatestStartAt    string                    `json:"latest_start_at,omitempty"`
		TargetVerified   bool                      `json:"target_verified"`
		ObservedEligible bool                      `json:"observed_eligible"`
		Blockers         []string                  `json:"blockers"`
		Options          jobRecoveryPreviewOptions `json:"options"`
		WaitTimeout      string                    `json:"wait_timeout,omitempty"`
		PollInterval     string                    `json:"poll_interval,omitempty"`
		ResultsDirectory string                    `json:"results_directory,omitempty"`
		RetryCommand     string                    `json:"retry_command,omitempty"`
		NextSteps        []jobRecoveryPreviewStep  `json:"next_steps,omitempty"`
		Note             string                    `json:"note"`
	}{true, false, name, runID, index, task.Status, task.Attempt, run.RetryMax, remaining, api.JobRetryDelay(task.Attempt).String(), now.Format(time.RFC3339Nano), run.ExecutionClass, run.LatestStartAt, true, len(blockers) == 0, blockers, options, waitTimeout, pollInterval, resultsDirectory, command, nextSteps,
		"This is a read-only observation, not a reservation. The server rechecks task state, retry budget, image readiness, start window, and current limits. Retrying executes the input again and may repeat effects. Waiting or results collection assesses the whole run. No retry, run record, or results file was created."}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Job task retry preview; no task retried.\nJob: %s\nRun: %s\nTask: %d (%s)\nAttempt: %d; retry maximum: %d; remaining budget: %d\nRetry delay: %s\nObserved eligible: %t\n", name, runID, index, task.Status, task.Attempt, run.RetryMax, remaining, report.RetryDelay, report.ObservedEligible)
	for _, blocker := range blockers {
		_, _ = fmt.Fprintln(osStdout, blocker)
	}
	if run.LatestStartAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Latest start: %s\n", oneLine(run.LatestStartAt))
	}
	if resultsDirectory != "" {
		_, _ = fmt.Fprintf(osStdout, "Results directory: %s\n", oneLine(resultsDirectory))
	}
	if command != "" {
		_, _ = fmt.Fprintf(osStdout, "Retry command (POSIX shells; starts another attempt):\n  %s\n", command)
	}
	renderJobRecoveryPreviewSteps(nextSteps)
	_, _ = fmt.Fprintln(osStdout, report.Note)
	return 0
}
