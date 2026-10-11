package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsUpdateInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs update NAME with explicit flags for scripts"))
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
		return printErr("Invalid Job", errors.New("selected Job identity changed"))
	}
	job = current
	if job.Status != "active" && job.Status != "paused" {
		return printErr("Job cannot be edited", errors.New("choose an active or paused Job"))
	}
	PrintProgress(osStdout, "Job: %s; image: %s; status: %s", job.Name, oneLine(job.ImageRef), oneLine(job.Status))
	maximum := func(limits []int) int {
		n := 0
		for _, v := range limits {
			if v > n {
				n = v
			}
		}
		return n
	}
	ram, err := scalePromptInt(ctx, prompt, "RAM per task (MB)", job.RAMMB, 1, maximum(api.JobRAMMB[:]))
	if err != nil {
		return startInputExit(err)
	}
	timeout, err := scalePromptInt(ctx, prompt, "Task timeout (seconds)", job.TaskTimeoutSec, 1, maximum(api.JobTaskTimeoutSec[:]))
	if err != nil {
		return startInputExit(err)
	}
	parallelism, err := scalePromptInt(ctx, prompt, "Maximum parallelism", job.MaxParallelism, 1, maximum(api.JobMaxParallelismPerRun[:]))
	if err != nil {
		return startInputExit(err)
	}
	retries, err := scalePromptInt(ctx, prompt, "Retry maximum", job.RetryMax, 0, maximum(api.JobMaxRetries[:]))
	if err != nil {
		return startInputExit(err)
	}
	defaultState := 0
	if job.Status == "paused" {
		defaultState = 1
	}
	state, err := prompt.choose(ctx, "Future dispatches", []string{"Active", "Paused"}, defaultState)
	if err != nil {
		return startInputExit(err)
	}
	status := []string{"active", "paused"}[state]
	PrintProgress(osStdout, "Current schedule: %s; timezone: %s", oneLine(job.Schedule), oneLine(job.Timezone))
	scheduleOptions := []string{"Keep current schedule", "Set or change schedule"}
	if job.Schedule != "" {
		scheduleOptions = append(scheduleOptions, "Remove schedule (convert to batch)")
	}
	scheduleChoice, err := prompt.choose(ctx, "Scheduling", scheduleOptions, 0)
	if err != nil {
		return startInputExit(err)
	}
	schedule, timezone := job.Schedule, job.Timezone
	switch scheduleChoice {
	case 1:
		schedule, timezone, err = promptJobSchedule(ctx, prompt, job.Schedule, job.Timezone)
		if err != nil {
			return startInputExit(err)
		}
	case 2:
		schedule = ""
	}
	req := api.UpdateJobRequest{}
	changes := 0
	if ram != job.RAMMB {
		req.RAMMB = &ram
		changes++
		PrintProgress(osStdout, "RAM: %d → %d MB", job.RAMMB, ram)
	}
	if timeout != job.TaskTimeoutSec {
		req.TaskTimeoutSec = &timeout
		changes++
		PrintProgress(osStdout, "Timeout: %d → %d seconds", job.TaskTimeoutSec, timeout)
	}
	if parallelism != job.MaxParallelism {
		req.MaxParallelism = &parallelism
		changes++
		PrintProgress(osStdout, "Parallelism: %d → %d", job.MaxParallelism, parallelism)
	}
	if retries != job.RetryMax {
		req.RetryMax = &retries
		changes++
		PrintProgress(osStdout, "Retry maximum: %d → %d", job.RetryMax, retries)
	}
	if status != job.Status {
		req.Status = &status
		changes++
		PrintProgress(osStdout, "Status: %s → %s", job.Status, status)
	}
	if schedule != job.Schedule {
		req.Schedule = &schedule
		changes++
		PrintProgress(osStdout, "Schedule: %s → %s", oneLine(job.Schedule), schedule)
	}
	if schedule != "" && timezone != job.Timezone {
		req.Timezone = &timezone
		changes++
		PrintProgress(osStdout, "Timezone: %s → %s", oneLine(job.Timezone), timezone)
	}
	if changes == 0 {
		PrintProgress(osStdout, "No changes to save.")
		return 0
	}
	if schedule != "" && (scheduleChoice == 1 || status != job.Status) {
		if err := previewJobSchedule(schedule, timezone); err != nil {
			return printErr("Could not preview schedule", err)
		}
		PrintProgress(osStdout, "Scheduled times are nominal; execution can be delayed or skipped under the Job's scheduling policy.")
		if status == "paused" {
			PrintProgress(osStdout, "The Job remains paused; no scheduled dispatches occur until resumed.")
		} else {
			PrintProgress(osStdout, "The active Job can dispatch runs on this schedule once its image is ready.")
		}
	}
	if schedule == "" && job.Schedule != "" {
		PrintProgress(osStdout, "Removing the schedule converts this Job to batch; existing runs continue independently.")
	}
	PrintProgress(osStdout, "Account plan limits are checked by the server. Pausing future dispatches does not cancel running tasks.")
	confirmed, err := prompt.confirm(ctx, "Save these Job changes?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Job update canceled.")
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
	defer cancel()
	updated, err := client.UpdateJob(writeCtx, job.Name, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if updated.ID != job.ID || updated.AccountID != job.AccountID || updated.Name != job.Name {
		return printErr("Invalid update receipt", errors.New("server returned a different Job"))
	}
	PrintOK(osStdout, "Job %s updated.", updated.Name)
	return 0
}
