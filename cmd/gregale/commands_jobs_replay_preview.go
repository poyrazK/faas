package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type jobRecoveryPreviewOptions struct {
	Wait           bool          `json:"wait"`
	CollectResults bool          `json:"collect_results"`
	WaitTimeout    time.Duration `json:"-"`
	PollInterval   time.Duration `json:"-"`
	ResultsParent  string        `json:"-"`
	Include        []string      `json:"include,omitempty"`
	Exclude        []string      `json:"exclude,omitempty"`
}

func previewJobReplay(ctx context.Context, client *api.Client, name, runID string, options jobRecoveryPreviewOptions) int {
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return printErr("Could not inspect replay Job", err)
	}
	if job.Name != name || !jobRunIDPattern.MatchString(job.ID) || job.AccountID == "" {
		return printErr("Invalid replay source", errors.New("job response did not identify the requested Job"))
	}
	run, err := client.GetJobRun(ctx, name, runID)
	if err != nil {
		return printErr("Could not inspect replay source", err)
	}
	if run.ID != runID || run.JobID != job.ID || run.AccountID != job.AccountID || run.Tasks < 0 || run.Tasks > 20000 {
		return printErr("Invalid replay source", errors.New("source run identity or task count is invalid; preview supports at most 20000 tasks"))
	}
	counts := map[string]int{}
	seen := map[int]bool{}
	unsuccessful, offset, complete := 0, 0, false
	for page := 0; page < 100; page++ {
		out, err := client.ListJobRunTasksPage(ctx, name, runID, 200, offset)
		if err != nil {
			return printErr("Could not inspect source tasks", err)
		}
		if len(out.Tasks) > 200 {
			return printErr("Invalid source tasks", errors.New("source task page exceeded its requested limit"))
		}
		for _, task := range out.Tasks {
			if task.RunID != runID || task.TaskIndex < 0 || task.TaskIndex >= run.Tasks || seen[task.TaskIndex] {
				return printErr("Invalid source tasks", errors.New("source task identity or index is invalid or repeated"))
			}
			switch task.Status {
			case "succeeded", "failed", "timeout", "oom", "cancelled", "queued", "claimed", "running":
			default:
				return printErr("Invalid source tasks", errors.New("source task has an unknown status"))
			}
			seen[task.TaskIndex] = true
			counts[task.Status]++
			if jobTaskRetryStatus(task.Status) {
				unsuccessful++
			}
		}
		if out.NextOffset < 0 {
			complete = len(seen) == run.Tasks
			break
		}
		if out.NextOffset <= offset || len(out.Tasks) == 0 {
			return printErr("Invalid source tasks", errors.New("source task pagination did not advance"))
		}
		offset = out.NextOffset
	}
	if !complete {
		return printErr("Incomplete source tasks", errors.New("source task inventory is incomplete or exceeded 100 pages; no replay preview was produced"))
	}
	blockers := []string{}
	var nextSteps []jobRecoveryPreviewStep
	if run.FinishedAt == "" || (run.AggregateStatus != "failed" && run.AggregateStatus != "cancelled" && run.AggregateStatus != "dead_letter" && run.AggregateStatus != "succeeded") || counts["queued"]+counts["claimed"]+counts["running"] > 0 {
		blockers = append(blockers, "Source run and all tasks must be terminal.")
		nextSteps = append(nextSteps, jobRecoveryPreviewStep{"Inspect current source status before replaying.", savedNameSuggestionCommand([]string{"jobs", "status", name, runID})})
	}
	if job.Status != "active" || job.ImageMaterializationStatus != "ready" || job.ImageStorageKey == "" || run.ImageRefSnapshot == "" || run.ImageRefSnapshot != job.ImageRef || run.ImageResolvedDigestSnapshot == "" || job.ImageResolvedDigest != run.ImageResolvedDigestSnapshot {
		blockers = append(blockers, "Replay requires an active Job with the same ready image digest as the source run.")
		nextSteps = append(nextSteps, jobRecoveryPreviewStep{"Inspect Job status and image readiness; this does not change the Job or image.", savedNameSuggestionCommand([]string{"jobs", "info", name})})
	}
	if unsuccessful == 0 {
		blockers = append(blockers, "No unsuccessful tasks to replay.")
		nextSteps = append(nextSteps, jobRecoveryPreviewStep{"Inspect all source task outcomes.", savedNameSuggestionCommand([]string{"jobs", "tasks", name, runID, "--all"})})
	}
	command := ""
	if len(blockers) == 0 {
		args := []string{"jobs", "replay-failed", name, runID}
		command = savedNameSuggestionCommand(append(args, jobRecoveryPreviewFollowArgs(options)...))
	}
	resultsTemplate, waitTimeout, pollInterval := "", "", ""
	if options.CollectResults {
		resultsTemplate = filepath.Join(options.ResultsParent, "NEW_RUN_ID")
	}
	if options.Wait {
		waitTimeout, pollInterval = options.WaitTimeout.String(), options.PollInterval.String()
	}
	report := struct {
		DryRun             bool                      `json:"dry_run"`
		Changed            bool                      `json:"changed"`
		Job                string                    `json:"job"`
		SourceRunID        string                    `json:"source_run_id"`
		SourceStatus       string                    `json:"source_status"`
		SourceVerified     bool                      `json:"source_verified"`
		ObservedEligible   bool                      `json:"observed_eligible"`
		Blockers           []string                  `json:"blockers"`
		TotalTasks         int                       `json:"total_tasks"`
		UnsuccessfulTasks  int                       `json:"unsuccessful_tasks"`
		TaskStatuses       map[string]int            `json:"task_statuses"`
		ImageDigest        string                    `json:"image_digest"`
		Parallelism        int                       `json:"parallelism"`
		TaskTimeoutSeconds int                       `json:"task_timeout_seconds"`
		RetryMax           int                       `json:"retry_max"`
		Options            jobRecoveryPreviewOptions `json:"options"`
		WaitTimeout        string                    `json:"wait_timeout,omitempty"`
		PollInterval       string                    `json:"poll_interval,omitempty"`
		ResultsTemplate    string                    `json:"results_directory_template,omitempty"`
		ReplayCommand      string                    `json:"replay_command,omitempty"`
		NextSteps          []jobRecoveryPreviewStep  `json:"next_steps,omitempty"`
		Note               string                    `json:"note"`
	}{true, false, name, runID, run.AggregateStatus, true, len(blockers) == 0, blockers, run.Tasks, unsuccessful, counts, run.ImageResolvedDigestSnapshot, run.Parallelism, run.TaskTimeoutSec, run.RetryMax, options, waitTimeout, pollInterval, resultsTemplate, command, nextSteps,
		"This is a read-only observation, not a reservation. Replayed inputs execute again and may repeat effects. The server rechecks eligibility and current limits when replay is requested. No run, record, or results file was created."}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Job replay preview; no run created.\nJob: %s\nSource run: %s (%s)\nUnsuccessful tasks: %d of %d\nImage digest: %s\nParallelism: %d; task timeout: %ds; retries: %d\nObserved eligible: %s\n", name, runID, oneLine(run.AggregateStatus), unsuccessful, run.Tasks, oneLine(run.ImageResolvedDigestSnapshot), run.Parallelism, run.TaskTimeoutSec, run.RetryMax, strconv.FormatBool(report.ObservedEligible))
	for _, blocker := range blockers {
		_, _ = fmt.Fprintln(osStdout, blocker)
	}
	if resultsTemplate != "" {
		_, _ = fmt.Fprintf(osStdout, "Results directory template (NEW_RUN_ID is assigned on acceptance): %s\n", oneLine(resultsTemplate))
	}
	if command != "" {
		_, _ = fmt.Fprintf(osStdout, "Replay command (POSIX shells; starts a new run):\n  %s\n", command)
	}
	renderJobRecoveryPreviewSteps(nextSteps)
	_, _ = fmt.Fprintln(osStdout, report.Note)
	return 0
}
