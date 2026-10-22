package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func previewJobCancel(ctx context.Context, client *api.Client, name, runID string) int {
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return printErr("Could not inspect cancellation Job", err)
	}
	if job.Name != name || !jobRunIDPattern.MatchString(job.ID) || job.AccountID == "" {
		return printErr("Invalid cancellation target", errors.New("job response did not identify the requested Job"))
	}
	run, err := client.GetJobRun(ctx, name, runID)
	if err != nil {
		return printErr("Could not inspect cancellation target", err)
	}
	if run.ID != runID || run.JobID != job.ID || run.AccountID != job.AccountID {
		return printErr("Invalid cancellation target", errors.New("source run did not match the requested Job and UUID"))
	}
	switch run.AggregateStatus {
	case "queued", "running", "succeeded", "failed", "cancelled", "dead_letter":
	default:
		return printErr("Invalid cancellation target", errors.New("source run has an unknown status"))
	}
	if run.Tasks < 0 || run.TasksRunning < 0 || run.TasksSucceeded < 0 || run.TasksFailed < 0 || run.TasksCancelled < 0 || run.DeadLetterCount < 0 || run.TasksRunning > run.Tasks || run.TasksSucceeded > run.Tasks || run.TasksFailed > run.Tasks || run.TasksCancelled > run.Tasks || run.DeadLetterCount > run.Tasks {
		return printErr("Invalid cancellation target", errors.New("source run has invalid task counts"))
	}
	active := jobRunUnfinished(run)
	command := ""
	effects := []string{}
	message := "Run is already terminal; nothing to cancel."
	if active {
		command = savedNameSuggestionCommand([]string{"jobs", "cancel", name, runID})
		message = "Cancellation would target this exact run; its state can change after the preview."
		effects = []string{"Stops pending work and requests termination of running tasks.", "Effects already produced by tasks remain; cancellation does not undo them."}
	}
	report := struct {
		DryRun          bool     `json:"dry_run"`
		Changed         bool     `json:"changed"`
		Job             string   `json:"job"`
		RunID           string   `json:"run_id"`
		Status          string   `json:"status"`
		TargetVerified  bool     `json:"target_verified"`
		WouldCancel     bool     `json:"would_cancel"`
		TotalTasks      int      `json:"total_tasks"`
		RunningTasks    int      `json:"running_tasks"`
		SucceededTasks  int      `json:"succeeded_tasks"`
		FailedTasks     int      `json:"failed_tasks"`
		CancelledTasks  int      `json:"cancelled_tasks"`
		DeadLetterTasks int      `json:"dead_letter_tasks"`
		Effects         []string `json:"effects"`
		Message         string   `json:"message"`
		CancelCommand   string   `json:"cancel_command,omitempty"`
		InspectCommand  string   `json:"inspect_command"`
		Note            string   `json:"note"`
	}{true, false, name, runID, run.AggregateStatus, true, active, run.Tasks, run.TasksRunning, run.TasksSucceeded, run.TasksFailed, run.TasksCancelled, run.DeadLetterCount, effects, message, command,
		savedNameSuggestionCommand([]string{"jobs", "status", name, runID}), "This is a read-only observation, not a reservation. No cancellation was submitted and no accepted-run record or result file was created."}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Job cancellation preview; no run cancelled.\nJob: %s\nRun: %s\nStatus: %s\nTasks: %d total; %d running; %d succeeded; %d failed; %d cancelled\nDead-letter: %d\n%s\n", name, runID, run.AggregateStatus, run.Tasks, run.TasksRunning, run.TasksSucceeded, run.TasksFailed, run.TasksCancelled, run.DeadLetterCount, message)
	for _, effect := range effects {
		_, _ = fmt.Fprintln(osStdout, effect)
	}
	if command != "" {
		_, _ = fmt.Fprintf(osStdout, "Cancellation command (POSIX shells; cancels this run):\n  %s\n", command)
	}
	_, _ = fmt.Fprintf(osStdout, "Inspect status (POSIX shells):\n  %s\n%s\n", report.InspectCommand, report.Note)
	return 0
}
