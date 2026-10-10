package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func jobTaskRetryStatus(status string) bool {
	return status == "failed" || status == "timeout" || status == "oom" || status == "cancelled"
}

func cmdJobsRetryInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs retry NAME RUN_ID TASK_INDEX for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, run, task, selected, err := chooseJobTask(ctx, client, prompt, true)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	currentRun, currentTask, err := readJobRetryTarget(readCtx, client, job, run.ID, task.TaskIndex)
	cancel()
	if err != nil {
		return printErr("Could not read retry target", err)
	}
	run, task = currentRun, currentTask
	PrintProgress(osStdout, "Job: %s; run: %s; task: %d\nStatus: %s; attempt: %d; retry maximum: %d\nError: %s", job.Name, run.ID, task.TaskIndex, oneLine(task.Status), task.Attempt, run.RetryMax, oneLine(task.ErrorMessage))
	if err := jobRetryEligibility(run, task, time.Now()); err != nil {
		return printErr("Task cannot be retried", err)
	}
	PrintProgress(osStdout, "The server will recheck the retry budget, image readiness, and start window when accepting this retry.")
	confirmed, err := prompt.confirm(ctx, "Retry this task once? This starts another execution and may repeat its effects.")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Task retry canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latestRun, latestTask, err := readJobRetryTarget(readCtx, client, job, run.ID, task.TaskIndex)
	cancel()
	if err != nil {
		return printErr("Could not recheck retry target", err)
	}
	if !reflect.DeepEqual(latestTask, task) || latestRun.RetryMax != run.RetryMax || latestRun.ExecutionClass != run.ExecutionClass || latestRun.LatestStartAt != run.LatestStartAt || latestRun.ImageRefSnapshot != run.ImageRefSnapshot || latestRun.ImageResolvedDigestSnapshot != run.ImageResolvedDigestSnapshot {
		return printErr("Retry target changed", errors.New("run the command again to review the current task and retry policy"))
	}
	if err := jobRetryEligibility(latestRun, latestTask, time.Now()); err != nil {
		return printErr("Task cannot be retried", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := client.RetryJobTask(writeCtx, job.Name, run.ID, task.TaskIndex)
	if err != nil {
		return printErr("Retry failed", err)
	}
	if resp.Run.ID != run.ID || resp.Run.JobID != job.ID || resp.Task.RunID != run.ID || resp.Task.TaskIndex != task.TaskIndex {
		return printErr("Invalid retry receipt", errors.New("server returned a different retry target; inspect the selected task before taking further action"))
	}
	PrintOK(osStdout, "Retried task %s/%d (attempt=%d, next_attempt_at=%s)", run.ID, task.TaskIndex, resp.Task.Attempt, resp.NextAttemptAt)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	_, _ = fmt.Fprintln(osStdout, "Inspection commands (POSIX shells):\n"+strings.Join(append(append([]string{}, command...), "jobs", "tasks", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID)), " ")+"\n"+strings.Join(append(command, "jobs", "logs", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), strconv.Itoa(task.TaskIndex)), " "))
	return 0
}

func jobRetryEligibility(run api.JobRunResponse, task api.JobTaskResponse, now time.Time) error {
	if !jobTaskRetryStatus(task.Status) {
		return errors.New("task is no longer failed, timed out, out of memory, or cancelled")
	}
	if task.Attempt > run.RetryMax {
		return errors.New("configured retry budget is exhausted")
	}
	if run.ExecutionClass == "flexible" {
		deadline, err := time.Parse(time.RFC3339Nano, run.LatestStartAt)
		if err != nil || !now.Add(api.JobRetryDelay(task.Attempt)).Before(deadline) {
			return errors.New("flexible start window does not permit another retry")
		}
	}
	return nil
}

func readJobRetryTarget(ctx context.Context, client *api.Client, job api.JobResponse, runID string, index int) (api.JobRunResponse, api.JobTaskResponse, error) {
	run, err := client.GetJobRun(ctx, job.Name, runID)
	if err != nil {
		return run, api.JobTaskResponse{}, err
	}
	if run.ID != runID || run.JobID != job.ID || run.AccountID != job.AccountID {
		return run, api.JobTaskResponse{}, errors.New("run no longer matches the selected Job")
	}
	offset := 0
	for page := 0; page < 100; page++ {
		tasks, err := client.ListJobRunTasksPage(ctx, job.Name, runID, 200, offset)
		if err != nil {
			return run, api.JobTaskResponse{}, err
		}
		for _, task := range tasks.Tasks {
			if task.RunID != runID {
				return run, task, errors.New("task belongs to another run")
			}
			if task.TaskIndex == index {
				return run, task, nil
			}
		}
		if tasks.NextOffset < 0 {
			break
		}
		if tasks.NextOffset <= offset {
			return run, api.JobTaskResponse{}, errors.New("invalid task page offset")
		}
		offset = tasks.NextOffset
	}
	return run, api.JobTaskResponse{}, errors.New("selected task was not found within the task page limit")
}
