package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func printDeploymentLogCommand(w io.Writer, app, deployment string) {
	_, _ = fmt.Fprintf(w, "  Deployment logs (POSIX shells):\n    %s logs %s --deployment %s\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(deployment))
}

func printJobHistoryNextSteps(w io.Writer, name string, runs []api.JobRunResponse) {
	printSucceededJobArtifactCommands(w, name, runs)
	if !jobSlugPattern.MatchString(name) {
		return
	}
	commands := make([]string, 0)
	seen := map[string]bool{}
	for _, run := range runs {
		if !jobRunIDPattern.MatchString(run.ID) || seen[run.ID] {
			continue
		}
		var command string
		switch run.AggregateStatus {
		case "failed", "dead_letter":
			command = fmt.Sprintf("%s jobs tasks %s %s --failed-only\n  %s jobs logs %s %s --failed-only", recoveryCLI(), recoveryArgument(name), recoveryArgument(run.ID), recoveryCLI(), recoveryArgument(name), recoveryArgument(run.ID))
			command += "\n  " + savedNameSuggestionCommand([]string{"jobs", "replay-failed", name, run.ID, "--dry-run"})
		case "queued", "running":
			command = fmt.Sprintf("%s jobs wait %s %s", recoveryCLI(), recoveryArgument(name), recoveryArgument(run.ID))
			command += "\n  " + savedNameSuggestionCommand([]string{"jobs", "cancel", name, run.ID, "--dry-run"})
		default:
			continue
		}
		seen[run.ID] = true
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nInspect failures, preview recovery/cancellation, or follow active runs (POSIX shells):")
	for i, command := range commands {
		if i == 3 {
			_, _ = fmt.Fprintf(w, "  %d more failed or active runs are listed above; use their UUIDs with jobs tasks --failed-only, jobs logs --failed-only, jobs replay-failed --dry-run, jobs wait, or jobs cancel --dry-run.\n", len(commands)-3)
			break
		}
		_, _ = fmt.Fprintf(w, "  %s\n", command)
	}
}

func printFailedWorkflowRunCommands(w io.Writer, runs []api.WorkflowRunResponse) {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, run := range runs {
		if (run.Status == "failed" || run.Status == "dead") && workflowUUIDPattern.MatchString(run.ID) && !seen[run.ID] {
			ids = append(ids, run.ID)
			seen[run.ID] = true
		}
	}
	if len(ids) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nInspect failed workflow runs (POSIX shells):")
	for i, id := range ids {
		if i == 3 {
			_, _ = fmt.Fprintf(w, "  %d more failed runs are listed above; use their UUIDs with workflows diagnose or workflows steps --failed-only.\n", len(ids)-3)
			break
		}
		_, _ = fmt.Fprintf(w, "  %s workflows diagnose %s\n  %s workflows steps %s --failed-only\n", recoveryCLI(), recoveryArgument(id), recoveryCLI(), recoveryArgument(id))
	}
}

func printActiveWorkflowRunCommands(w io.Writer, runs []api.WorkflowRunResponse) {
	active := make([]api.WorkflowRunResponse, 0)
	seen := map[string]bool{}
	for _, run := range runs {
		if run.CancelledAt != nil || !workflowUUIDPattern.MatchString(run.ID) || seen[run.ID] {
			continue
		}
		if run.Status == "pending" || run.Status == "running" || run.Status == "awaiting_event" {
			active = append(active, run)
			seen[run.ID] = true
		}
	}
	if len(active) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nFollow active workflow runs (POSIX shells):")
	for i, run := range active {
		if i == 3 {
			_, _ = fmt.Fprintf(w, "  %d more active runs are listed above; use their UUIDs with workflows wait.\n", len(active)-3)
			break
		}
		_, _ = fmt.Fprintf(w, "  %s workflows wait %s\n", recoveryCLI(), recoveryArgument(run.ID))
		switch run.Status {
		case "pending":
			_, _ = fmt.Fprintln(w, "    Queued: execution has not started yet; waiting observes this run.")
		case "running":
			_, _ = fmt.Fprintln(w, "    Executing: this run has not completed yet; waiting follows its recorded state.")
		case "awaiting_event":
			_, _ = fmt.Fprintln(w, "    Awaiting an event: waiting only observes this run; it does not send an event or advance it.")
		}
	}
}

func printFailedJobTaskLogs(w io.Writer, name, runID string, tasks []api.JobTaskResponse) {
	indexes := make([]int, 0)
	seen := map[int]bool{}
	retryPreview := map[int]bool{}
	hasCancelled := false
	for _, task := range tasks {
		if task.RunID != runID || task.TaskIndex < 0 || seen[task.TaskIndex] {
			continue
		}
		if jobTaskRetryStatus(task.Status) || task.Status == "dead_letter" {
			indexes = append(indexes, task.TaskIndex)
			seen[task.TaskIndex] = true
			retryPreview[task.TaskIndex] = jobTaskRetryStatus(task.Status)
			hasCancelled = hasCancelled || task.Status == "cancelled"
		}
	}
	sort.Ints(indexes)
	if len(indexes) == 0 {
		return
	}
	heading, description := "Failed", "failed"
	if hasCancelled {
		heading, description = "Unsuccessful", "unsuccessful"
	}
	_, _ = fmt.Fprintf(w, "\n%s task logs, retained attempts, and retry previews (POSIX shells):\n", heading)
	for i, index := range indexes {
		if i == 3 {
			_, _ = fmt.Fprintf(w, "  %d more %s tasks are listed above; use their task indexes with jobs logs, jobs attempts --all, or jobs retry --task N --dry-run.\n", len(indexes)-3, description)
			break
		}
		_, _ = fmt.Fprintf(w, "  %s jobs logs %s %s %d\n", recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), index)
		_, _ = fmt.Fprintf(w, "  %s jobs attempts %s %s %d --all\n", recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), index)
		if retryPreview[index] {
			_, _ = fmt.Fprintf(w, "  %s\n", savedNameSuggestionCommand([]string{"jobs", "retry", name, runID, "--task", strconv.Itoa(index), "--dry-run"}))
		}
	}
	_, _ = fmt.Fprintln(w, "Logs depend on retention; use jobs attempts for retained outcomes from earlier attempts.")
	_, _ = fmt.Fprintln(w, "Retry previews read current task state without retrying; the server rechecks eligibility when a retry is requested.")
}

func printWorkflowFailureEvidence(w io.Writer, run api.WorkflowRunResponse) {
	if run.Status != "failed" && run.Status != "dead" {
		return
	}
	_, _ = fmt.Fprintf(w, "\nFailure evidence (POSIX shells):\n  %s workflows diagnose %s\n", recoveryCLI(), recoveryArgument(run.ID))
	if run.CurrentStep != nil && *run.CurrentStep != "" {
		_, _ = fmt.Fprintf(w, "  %s workflows attempts %s %s\n", recoveryCLI(), recoveryArgument(run.ID), recoveryArgument(*run.CurrentStep))
	} else {
		_, _ = fmt.Fprintf(w, "  %s workflows steps %s\n", recoveryCLI(), recoveryArgument(run.ID))
	}
}

func printSucceededJobArtifactCommands(w io.Writer, name string, runs []api.JobRunResponse) {
	if !jobSlugPattern.MatchString(name) {
		return
	}
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, run := range runs {
		if run.AggregateStatus != "succeeded" || !jobRunIDPattern.MatchString(run.ID) || seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		ids = append(ids, run.ID)
	}
	if len(ids) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nFind and collect recorded artifacts from succeeded runs (POSIX shells):")
	for i, id := range ids {
		if i == 3 {
			_, _ = fmt.Fprintf(w, "  %d more succeeded runs are listed above; use their UUIDs with jobs tasks --artifacts-only or jobs results.\n", len(ids)-3)
			break
		}
		_, _ = fmt.Fprintf(w, "  %s jobs tasks %s %s --artifacts-only\n  %s jobs results %s %s\n", recoveryCLI(), recoveryArgument(name), recoveryArgument(id), recoveryCLI(), recoveryArgument(name), recoveryArgument(id))
	}
	_, _ = fmt.Fprintln(w, "Succeeded runs may have no recorded artifacts. Task discovery checks current manifests; results collection saves managed outputs using matching local defaults and a folder per run.")
}
