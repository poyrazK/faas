package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsAttemptsInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs attempts NAME RUN_ID TASK_INDEX for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, run, task, selected, err := chooseJobTask(ctx, client, prompt, false)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "attempts", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), strconv.Itoa(task.TaskIndex))
	_, _ = fmt.Fprintln(osStdout, "Attempt-history command (POSIX shells):\n"+strings.Join(command, " "))
	var attempts []api.JobTaskAttemptResponse
	choice, err := chooseJobLogPage(ctx, prompt, "Choose a retained attempt to inspect.", func(ctx context.Context, offset int) ([]string, int, error) {
		page, err := client.ListJobTaskAttemptsPage(ctx, job.Name, run.ID, task.TaskIndex, 20, offset)
		attempts = page.Attempts
		labels := []string{}
		seen := map[int]bool{}
		for _, attempt := range attempts {
			if attempt.RunID != run.ID || attempt.TaskIndex != task.TaskIndex || attempt.Attempt < 1 || seen[attempt.Attempt] {
				return nil, -1, errors.New("attempt history does not match the selected task or repeats an attempt")
			}
			seen[attempt.Attempt] = true
			labels = append(labels, fmt.Sprintf("Attempt %d · %s · %s · %s", attempt.Attempt, oneLine(attempt.Status), oneLine(attempt.FinishedAt), oneLine(attempt.ErrorMessage)))
		}
		return labels, page.NextOffset, err
	})
	if err != nil {
		return jobLogPickerError(err)
	}
	if choice < 0 {
		return 0
	}
	attempt := attempts[choice]
	exitCode := "-"
	if attempt.ExitCode != nil {
		exitCode = strconv.Itoa(*attempt.ExitCode)
	}
	PrintProgress(osStdout, "Job: %s; run: %s; task: %d; retained attempt: %d\nStatus: %s; exit code: %s\nStarted: %s; finished: %s\nOutcome: %s; decision: %s\nError class: %s\nError: %s", job.Name, run.ID, task.TaskIndex, attempt.Attempt, oneLine(attempt.Status), exitCode, oneLine(attempt.StartedAt), oneLine(attempt.FinishedAt), oneLine(attempt.OutcomeCode), oneLine(workDecisionLabel(attempt.WorkDecision)), oneLine(attempt.ErrorClass), oneLine(attempt.ErrorMessage))
	show, err := prompt.confirm(ctx, "Show retained output from this attempt?")
	if err != nil {
		return startInputExit(err)
	}
	if show {
		return renderJobTaskLogs(api.JobTaskLogResponse{TaskStatus: attempt.Status, LogContent: attempt.LogContent, Truncated: attempt.LogTruncated})
	}
	return 0
}
