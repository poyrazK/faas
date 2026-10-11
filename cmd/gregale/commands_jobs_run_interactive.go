package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsRunInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs run NAME --tasks N for scripts"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	job, selected, err := chooseJob(ctx, client, prompt)
	if err != nil {
		return jobLogPickerError(err)
	}
	if !selected {
		return 0
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	current, err := client.GetJob(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if current.ID != job.ID || current.AccountID != job.AccountID || current.Name != job.Name {
		return printErr("Job changed", errors.New("the selected Job identity changed"))
	}
	job = current
	if job.Status != "active" || job.ImageMaterializationStatus != "ready" {
		return printErr("Job not ready", errors.New("the Job must be active with a ready image before starting a run"))
	}
	maxTasks, maxParallelism := 1, 1
	for _, limit := range api.JobMaxTasksPerRun {
		if limit > maxTasks {
			maxTasks = limit
		}
	}
	for _, limit := range api.JobMaxParallelismPerRun {
		if limit > maxParallelism {
			maxParallelism = limit
		}
	}
	tasks, err := scalePromptInt(ctx, prompt, "Task count", 1, 1, maxTasks)
	if err != nil {
		return startInputExit(err)
	}
	parallelism, err := scalePromptInt(ctx, prompt, "Parallelism", job.MaxParallelism, 1, maxParallelism)
	if err != nil {
		return startInputExit(err)
	}
	PrintProgress(osStdout, "Job: %s\nImage: %s\nCommand: %s\nTasks: %d; parallelism: %d\nRAM: %d MB per task; timeout: %d seconds; retry maximum: %d", job.Name, oneLine(job.ImageRef), oneLine(strings.Join(job.Command, " ")), tasks, parallelism, job.RAMMB, job.TaskTimeoutSec, job.RetryMax)
	if job.FailureRules != nil {
		PrintProgress(osStdout, "This Job also has custom failure/retry rules.")
	}
	PrintProgress(osStdout, "The run inherits the Job environment and policy. Account plan limits are checked by the server.")
	confirmed, err := prompt.confirm(ctx, "Start this run? It executes the Job command for each task.")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Job run canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.GetJob(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not recheck Job", err)
	}
	if !reflect.DeepEqual(jobRunConfig(latest), jobRunConfig(job)) {
		return printErr("Job changed", errors.New("run the command again to review current settings"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	run, err := client.CreateJobRun(writeCtx, job.Name, api.CreateJobRunRequest{Tasks: tasks, Parallelism: &parallelism})
	cancel()
	if err != nil {
		return printErr("Run creation failed", err)
	}
	if !jobRunIDPattern.MatchString(run.ID) || run.JobID != job.ID || run.AccountID != job.AccountID {
		return printErr("Invalid run receipt", errors.New("server returned a different Job; inspect runs before starting another run"))
	}
	PrintOK(osStdout, "Run %s dispatched (%d tasks, parallelism=%d)", run.ID, run.Tasks, run.Parallelism)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "wait", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), "--timeout", "10m")
	_, _ = fmt.Fprintln(osStdout, "Follow command (POSIX shells):\n"+strings.Join(command, " "))
	follow, err := prompt.confirm(ctx, "Follow this run for up to 10 minutes?")
	if err != nil {
		return startInputExit(err)
	}
	if !follow {
		return 0
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, code, err := followJobRun(waitCtx, client, job, run.ID, 2*time.Second)
	if err != nil {
		printErr("Job wait ended", err)
	}
	return code
}

func jobRunConfig(job api.JobResponse) api.JobResponse {
	job.CreatedAt, job.UpdatedAt, job.LastScheduledAt = "", "", ""
	return job
}
