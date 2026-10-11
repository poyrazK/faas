package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"
)

func cmdJobsRmInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs rm NAME for scripts"))
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
		return printErr("Could not read removal target", err)
	}
	if current.ID != job.ID || current.AccountID != job.AccountID || current.Name != job.Name {
		return printErr("Job changed", errors.New("the selected Job identity changed; run the command again"))
	}
	job = current
	if job.Status == "deleted" {
		PrintProgress(osStdout, "Job %s is already removed; no deletion submitted.", job.Name)
		return 0
	}
	if job.Status != "active" && job.Status != "paused" {
		return printErr("Unknown Job state", errors.New("inspect the Job before removing it"))
	}
	command, err := json.Marshal(job.Command)
	if err != nil {
		return printErr("Could not render Job command", err)
	}
	PrintProgress(osStdout, "Remove Job: %s\nImage: %s\nCommand arguments: %s\nState: %s; kind: %s\nRAM: %d MB; timeout: %d seconds; parallelism: %d; retry maximum: %d", job.Name, oneLine(job.ImageRef), command, oneLine(job.Status), oneLine(job.Kind), job.RAMMB, job.TaskTimeoutSec, job.MaxParallelism, job.RetryMax)
	if job.Schedule != "" {
		PrintProgress(osStdout, "Recurring schedule: %s; timezone: %s", oneLine(job.Schedule), oneLine(job.Timezone))
	}
	PrintProgress(osStdout, "Removal soft-deletes this Job and stops future dispatches. The server blocks removal while queued or running tasks remain; cancel or wait for them first.")
	confirmed, err := prompt.confirm(ctx, "Remove this Job?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Job removal canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.GetJob(readCtx, job.Name)
	cancel()
	if err != nil {
		return printErr("Could not recheck removal target", err)
	}
	if latest.ID != job.ID || latest.AccountID != job.AccountID || latest.Name != job.Name {
		return printErr("Job changed", errors.New("run the command again to review the current Job"))
	}
	if latest.Status == "deleted" {
		PrintProgress(osStdout, "Job already removed; no deletion submitted.")
		return 0
	}
	if !reflect.DeepEqual(jobRunConfig(latest), jobRunConfig(job)) {
		return printErr("Job configuration changed", errors.New("run the command again to review current settings before removal"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := client.DeleteJob(writeCtx, job.Name); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Removed Job %s.", job.Name)
	return 0
}
