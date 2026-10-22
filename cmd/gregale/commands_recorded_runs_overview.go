package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recordedRunStatus struct {
	Record        acceptedRunRecord `json:"record"`
	Status        string            `json:"status,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	ResumeCommand string            `json:"resume_command"`
	NextAction    string            `json:"next_action"`
	NextCommand   string            `json:"next_command"`
}

type recordedRunsOverview struct {
	InProgress  []recordedRunStatus `json:"in_progress"`
	Finished    []recordedRunStatus `json:"finished"`
	Unavailable []recordedRunStatus `json:"unavailable"`
}

func recordedRunResumeCommand(record acceptedRunRecord) string {
	command := "FAAS_API=" + recoveryArgument(record.API) + " gregale --profile " + recoveryArgument(record.Profile)
	if record.Kind == "job" {
		command += " jobs resume"
	} else {
		command += " workflows resume-wait"
	}
	return command + " --run " + recoveryArgument(record.RunID)
}

func readRecordedRunStatus(ctx context.Context, client *api.Client, record acceptedRunRecord) recordedRunStatus {
	row := recordedRunStatus{Record: record, ResumeCommand: recordedRunResumeCommand(record)}
	var err error
	if record.Kind == "job" {
		var run api.JobRunResponse
		run, err = client.GetJobRun(ctx, record.Name, record.RunID)
		if err == nil {
			if run.ID != record.RunID || run.JobID != record.JobID || run.AccountID != record.AccountID {
				row.Reason = "Server response did not match the recorded Job identity."
				return row
			}
			row.Status = run.AggregateStatus
		}
	} else {
		var run api.WorkflowRunResponse
		run, err = client.GetWorkflowRun(ctx, record.RunID)
		if err == nil {
			if run.ID != record.RunID || run.WorkflowName != record.Name || record.AppID != "" && run.AppID != record.AppID {
				row.Reason = "Server response did not match the recorded Workflow identity."
				return row
			}
			row.Status = run.Status
			if run.CancelledAt != nil {
				row.Status = "cancelled"
			}
		}
	}
	if err != nil {
		row.Reason = "Run status could not be read; the local record was retained."
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == 404 {
			row.Reason = "Run is unavailable on this connection; the response does not establish whether it expired or was removed."
		}
	}
	return row
}

func overviewRecordedRuns(records []acceptedRunRecord, timeout time.Duration) int {
	report := recordedRunsOverview{InProgress: []recordedRunStatus{}, Finished: []recordedRunStatus{}, Unavailable: []recordedRunStatus{}}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	var client *api.Client
	var clientErr error
	loadedClient := false
	for _, record := range records {
		row := recordedRunStatus{Record: record, ResumeCommand: recordedRunResumeCommand(record)}
		switch {
		case ctx.Err() != nil:
			row.Reason = "Overview stopped before this run could be read; record retained."
		case record.Profile != currentProfile() || record.API != apiBase():
			row.Reason = "Recorded API or profile differs from the current connection; no request was made."
		default:
			if !loadedClient {
				client, clientErr = authedClient()
				loadedClient = true
			}
			if clientErr != nil {
				row.Reason = "Authentication is unavailable for the current connection; no request was made."
			} else {
				readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
				row = readRecordedRunStatus(readCtx, client, record)
				readCancel()
			}
		}
		setRecordedRunNextAction(&row)
		if row.Reason != "" {
			report.Unavailable = append(report.Unavailable, row)
			continue
		}
		switch recordedRunGroup(record.Kind, row.Status) {
		case "in_progress":
			report.InProgress = append(report.InProgress, row)
		case "finished":
			report.Finished = append(report.Finished, row)
		default:
			row.Reason = "The server returned an unrecognized status; outcome is not established."
			report.Unavailable = append(report.Unavailable, row)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderRecordedRunGroup("In progress", report.InProgress)
		renderRecordedRunGroup("Finished", report.Finished)
		renderRecordedRunGroup("Unavailable", report.Unavailable)
		_, _ = fmt.Fprintln(osStdout, "All local records were retained. Finished describes a terminal outcome, including failure; it does not mean success.")
	}
	if ctx.Err() == context.Canceled {
		return 130
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 124
	}
	if len(report.Unavailable) > 0 {
		return 1
	}
	return 0
}

// Recommend only from verified observations. Unavailable rows keep an exact
// observation command rather than guessing a failure or successful result.
func setRecordedRunNextAction(row *recordedRunStatus) {
	row.NextAction, row.NextCommand = "observe", row.ResumeCommand
	if row.Reason != "" {
		return
	}
	if row.Record.Kind == "job" {
		switch row.Status {
		case "succeeded":
			row.NextAction, row.NextCommand = "results", row.ResumeCommand+" --results"
		case "failed", "dead_letter":
			row.NextAction, row.NextCommand = "logs", row.ResumeCommand+" --logs"
		}
	} else {
		switch row.Status {
		case "failed", "dead", "awaiting_event":
			row.NextAction, row.NextCommand = "diagnose", row.ResumeCommand+" --diagnose"
		}
	}
}

func recordedRunGroup(kind, status string) string {
	if kind == "job" {
		switch status {
		case "queued", "running":
			return "in_progress"
		case "succeeded", "failed", "cancelled", "dead_letter":
			return "finished"
		}
	} else {
		switch status {
		case "pending", "running", "awaiting_event":
			return "in_progress"
		case "succeeded", "failed", "cancelled", "dead":
			return "finished"
		}
	}
	return "unavailable"
}

func renderRecordedRunGroup(label string, rows []recordedRunStatus) {
	_, _ = fmt.Fprintf(osStdout, "%s (%d):\n", label, len(rows))
	for _, row := range rows {
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %s; run=%s\n", row.Record.Kind, oneLine(row.Record.Name), oneLine(row.Status), row.Record.RunID)
		if row.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, "    %s\n", row.Reason)
		}
		label := "Observe this exact run"
		switch row.NextAction {
		case "results":
			label = "Collect results for this run"
		case "logs":
			label = "Read failed-task logs for this run"
		case "diagnose":
			label = "Read diagnostics for this workflow run"
		}
		_, _ = fmt.Fprintf(osStdout, "    %s (POSIX shells):\n      %s\n", label, row.NextCommand)
	}
}
