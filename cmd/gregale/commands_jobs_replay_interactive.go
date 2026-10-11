package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsReplayFailedInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs replay-failed NAME RUN_ID for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, source, selected, err := chooseJobRun(ctx, client, prompt)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	read := func() (api.JobResponse, api.JobRunResponse, []api.JobTaskResponse, error) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		currentJob, err := client.GetJob(readCtx, job.Name)
		if err != nil {
			return currentJob, source, nil, err
		}
		run, err := client.GetJobRun(readCtx, job.Name, source.ID)
		if err != nil {
			return currentJob, run, nil, err
		}
		if currentJob.ID != job.ID || currentJob.Name != job.Name || currentJob.AccountID != job.AccountID || run.ID != source.ID || run.JobID != job.ID || run.AccountID != job.AccountID {
			return currentJob, run, nil, errors.New("job or source run identity changed")
		}
		if run.AggregateStatus != "failed" && run.AggregateStatus != "cancelled" && run.AggregateStatus != "dead_letter" && run.AggregateStatus != "succeeded" {
			return currentJob, run, nil, errors.New("source run must be terminal")
		}
		if currentJob.Status != "active" || currentJob.ImageMaterializationStatus != "ready" || currentJob.ImageStorageKey == "" || run.FinishedAt == "" || run.ImageRefSnapshot != currentJob.ImageRef || run.ImageResolvedDigestSnapshot == "" || currentJob.ImageResolvedDigest != run.ImageResolvedDigestSnapshot {
			return currentJob, run, nil, errors.New("replay requires the same ready image digest as the source run")
		}
		tasks := []api.JobTaskResponse{}
		offset := 0
		seen := map[int]bool{}
		for page := 0; page < 100; page++ {
			out, err := client.ListJobRunTasksPage(readCtx, job.Name, run.ID, 200, offset)
			if err != nil {
				return currentJob, run, nil, err
			}
			for _, task := range out.Tasks {
				if task.RunID != run.ID || task.TaskIndex < 0 || seen[task.TaskIndex] {
					return currentJob, run, nil, errors.New("invalid or repeated source task")
				}
				seen[task.TaskIndex] = true
				tasks = append(tasks, task)
			}
			if out.NextOffset < 0 {
				if len(tasks) != run.Tasks {
					return currentJob, run, nil, errors.New("source task inventory is incomplete")
				}
				sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskIndex < tasks[j].TaskIndex })
				return currentJob, run, tasks, nil
			}
			if out.NextOffset <= offset {
				return currentJob, run, nil, errors.New("invalid task pagination")
			}
			offset = out.NextOffset
		}
		return currentJob, run, nil, errors.New("source task page limit reached")
	}
	currentJob, currentRun, tasks, err := read()
	if err != nil {
		return printErr("Source cannot be replayed", err)
	}
	count := 0
	for _, task := range tasks {
		if jobTaskRetryStatus(task.Status) {
			count++
			PrintProgress(osStdout, "Task %d: %s; input %s; error %s", task.TaskIndex, oneLine(task.Status), oneLine(task.InputID), oneLine(task.ErrorMessage))
		}
	}
	if count == 0 {
		PrintProgress(osStdout, "No unsuccessful tasks to replay.")
		return 0
	}
	PrintProgress(osStdout, "Create a linked recovery run from %s: %d unsuccessful tasks; image digest %s; parallelism %d; timeout %d seconds; retries %d.", source.ID, count, currentRun.ImageResolvedDigestSnapshot, currentRun.Parallelism, currentRun.TaskTimeoutSec, currentRun.RetryMax)
	PrintProgress(osStdout, "Replayed inputs execute again and may repeat effects. The server enforces current plan limits and selects source tasks atomically.")
	confirmed, err := prompt.confirm(ctx, "Create this recovery run?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Replay canceled.")
		return 0
	}
	latestJob, latestRun, latestTasks, err := read()
	if err != nil {
		return printErr("Could not recheck replay source", err)
	}
	if !reflect.DeepEqual(jobRunConfig(latestJob), jobRunConfig(currentJob)) || !reflect.DeepEqual(latestRun, currentRun) || !reflect.DeepEqual(latestTasks, tasks) {
		return printErr("Replay source changed", errors.New("run the command again to review current source tasks and policy"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	replay, err := client.ReplayFailedJobRun(writeCtx, job.Name, source.ID)
	cancel()
	if err != nil {
		return printErr("Replay failed", err)
	}
	if !jobRunIDPattern.MatchString(replay.ID) || replay.SourceRunID != source.ID || replay.JobID != job.ID || replay.AccountID != job.AccountID {
		return printErr("Invalid replay receipt", errors.New("server returned another recovery run; inspect Job runs before replaying again"))
	}
	PrintOK(osStdout, "Run %s created from %s (%d tasks).", replay.ID, source.ID, replay.Tasks)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "wait", quoteLogCommandArg(job.Name), quoteLogCommandArg(replay.ID), "--timeout", "10m")
	_, _ = fmt.Fprintln(osStdout, "Follow command (POSIX shells):\n"+strings.Join(command, " "))
	follow, err := prompt.confirm(ctx, "Follow the recovery run for up to 10 minutes?")
	if err != nil {
		return startInputExit(err)
	}
	if !follow {
		return 0
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, code, err := followJobRun(waitCtx, client, job, replay.ID, 2*time.Second)
	if err != nil {
		printErr("Job wait ended", err)
	}
	return code
}
