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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsTasksInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs tasks NAME RUN_ID for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, run, selected, err := chooseJobRun(ctx, client, prompt)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	filter, err := prompt.choose(ctx, "Tasks to browse", []string{"All tasks", "Unsuccessful tasks (failed, timeout, OOM, cancelled)"}, 0)
	if err != nil {
		return startInputExit(err)
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	offset := 0
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		current, err := client.GetJobRun(readCtx, job.Name, run.ID)
		if err != nil {
			cancel()
			return printErr("Could not read run", err)
		}
		if current.ID != run.ID || current.JobID != job.ID || current.AccountID != job.AccountID {
			cancel()
			return printErr("Invalid run", errors.New("server returned another run or Job"))
		}
		page, err := client.ListJobRunTasksPage(readCtx, job.Name, run.ID, 20, offset)
		cancel()
		if err != nil {
			return printErr("Could not read task page", err)
		}
		PrintProgress(osStdout, "Run %s: %s; running=%d succeeded=%d failed=%d cancelled=%d total=%d", run.ID, oneLine(current.AggregateStatus), current.TasksRunning, current.TasksSucceeded, current.TasksFailed, current.TasksCancelled, current.Tasks)
		tasks := []api.JobTaskResponse{}
		seen := map[int]bool{}
		for _, task := range page.Tasks {
			if task.RunID != run.ID || task.TaskIndex < 0 || seen[task.TaskIndex] {
				return printErr("Invalid task page", errors.New("task does not match the selected run or repeats an index"))
			}
			seen[task.TaskIndex] = true
			if filter == 1 && !jobTaskRetryStatus(task.Status) {
				continue
			}
			tasks = append(tasks, task)
		}
		labels := []string{}
		for _, task := range tasks {
			labels = append(labels, fmt.Sprintf("Task %d · %s · attempt %d · %s", task.TaskIndex, oneLine(task.Status), task.Attempt, oneLine(task.ErrorMessage)))
		}
		more := page.NextOffset >= 0
		if len(tasks) == 0 {
			PrintProgress(osStdout, "No matching tasks on this page.")
		}
		if more {
			labels = append(labels, "Show next task page")
		}
		refreshIndex := len(labels)
		labels = append(labels, "Refresh this page", "Change task filter", "Cancel")
		choice, err := prompt.choose(ctx, "Select a task or browser action.", labels, 0)
		if err != nil {
			return startInputExit(err)
		}
		if choice == len(labels)-1 {
			return 0
		}
		if choice == refreshIndex {
			continue
		}
		if choice == refreshIndex+1 {
			filter = 1 - filter
			offset = 0
			continue
		}
		if more && choice == len(tasks) {
			if page.NextOffset <= offset {
				return printErr("Invalid task pagination", errors.New("server repeated or moved backwards in task pages"))
			}
			offset = page.NextOffset
			continue
		}
		task := tasks[choice]
		exitCode := "-"
		if task.FinishedAt != "" {
			exitCode = strconv.Itoa(task.ExitCode)
		}
		PrintProgress(osStdout, "Task %d: status=%s; attempt=%d; exit code=%s\nError class: %s\nOutcome: %s; decision: %s\nError: %s\nStarted: %s; finished: %s", task.TaskIndex, oneLine(task.Status), task.Attempt, exitCode, oneLine(task.ErrorClass), oneLine(task.OutcomeCode), oneLine(workDecisionLabel(task.WorkDecision)), oneLine(task.ErrorMessage), oneLine(task.StartedAt), oneLine(task.FinishedAt))
		for _, sub := range []string{"logs", "attempts"} {
			inspect := append(append([]string{}, command...), "jobs", sub, quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), strconv.Itoa(task.TaskIndex))
			_, _ = fmt.Fprintln(osStdout, strings.Join(inspect, " "))
		}
		again, err := prompt.confirm(ctx, "Continue browsing this run?")
		if err != nil {
			return startInputExit(err)
		}
		if !again {
			return 0
		}
	}
	return printErr("Task browser limit reached", errors.New("rerun the interactive browser or use explicit task commands"))
}
