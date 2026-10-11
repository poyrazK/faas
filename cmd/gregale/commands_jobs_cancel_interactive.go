package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func jobRunUnfinished(run api.JobRunResponse) bool {
	return run.AggregateStatus == "queued" || run.AggregateStatus == "running"
}

func cmdJobsCancelInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs cancel NAME RUN_ID for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, run, selected, err := chooseJobRunFiltered(ctx, client, prompt, true)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	read := func() (api.JobRunResponse, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		current, err := client.GetJobRun(readCtx, job.Name, run.ID)
		if err == nil && (current.ID != run.ID || current.JobID != job.ID || current.AccountID != job.AccountID) {
			err = errors.New("run does not match the selected Job and run ID")
		}
		return current, err
	}
	current, err := read()
	if err != nil {
		return printErr("Could not read cancellation target", err)
	}
	if !jobRunUnfinished(current) {
		PrintProgress(osStdout, "Run %s is already %s; no cancellation submitted.", run.ID, oneLine(current.AggregateStatus))
		return 0
	}
	PrintProgress(osStdout, "Job: %s; run: %s\nStatus: %s; running=%d succeeded=%d failed=%d cancelled=%d total=%d", job.Name, run.ID, oneLine(current.AggregateStatus), current.TasksRunning, current.TasksSucceeded, current.TasksFailed, current.TasksCancelled, current.Tasks)
	PrintProgress(osStdout, "Cancellation stops pending work and requests termination of running tasks. Effects already produced by tasks remain.")
	confirmed, err := prompt.confirm(ctx, "Cancel this run?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Run cancellation declined.")
		return 0
	}
	latest, err := read()
	if err != nil {
		return printErr("Could not recheck cancellation target", err)
	}
	if !jobRunUnfinished(latest) {
		PrintProgress(osStdout, "Run %s is now %s; no cancellation submitted.", run.ID, oneLine(latest.AggregateStatus))
		return 0
	}
	if latest.AggregateStatus != current.AggregateStatus || latest.Tasks != current.Tasks || latest.TasksRunning != current.TasksRunning || latest.TasksSucceeded != current.TasksSucceeded || latest.TasksFailed != current.TasksFailed || latest.TasksCancelled != current.TasksCancelled || latest.DeadLetterCount != current.DeadLetterCount {
		return printErr("Run progress changed", errors.New("run the command again to review current task counts before canceling"))
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "tasks", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID))
	_, _ = fmt.Fprintln(osStdout, "Inspection command (POSIX shells):\n"+strings.Join(command, " "))
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := client.CancelJobRun(writeCtx, job.Name, run.ID)
	if err != nil {
		return printErr("Cancel failed", err)
	}
	if resp.Run.ID != run.ID || resp.Run.JobID != job.ID || resp.Run.AccountID != job.AccountID {
		return printErr("Invalid cancellation receipt", errors.New("server returned another run; inspect the selected run before taking further action"))
	}
	PrintOK(osStdout, "Cancellation recorded for run %s (status=%s).", resp.Run.ID, oneLine(resp.Run.AggregateStatus))
	return 0
}
