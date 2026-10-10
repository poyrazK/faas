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

	"github.com/onebox-faas/faas/pkg/jobresult"
)

func cmdJobsArtifactURLInteractive() int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use jobs artifact-url NAME RUN_ID TASK_INDEX ARTIFACT for scripts"))
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
	if len(task.OutputManifest) == 0 || string(task.OutputManifest) == "null" {
		PrintProgress(osStdout, "No output manifest for this task.")
		return 0
	}
	manifest, err := jobresult.Validate(task.OutputManifest)
	if err != nil {
		return printErr("Invalid output manifest", err)
	}
	artifacts := []jobresult.Artifact{}
	labels := []string{}
	external := 0
	for _, artifact := range manifest.Artifacts {
		if !strings.HasPrefix(artifact.URI, "obj://") {
			external++
			continue
		}
		artifacts = append(artifacts, artifact)
		labels = append(labels, fmt.Sprintf("%s · %d bytes · %s", oneLine(artifact.Name), artifact.SizeBytes, artifact.SHA256))
	}
	if external > 0 {
		PrintProgress(osStdout, "%d external-storage artifacts require access through their storage provider.", external)
	}
	if len(artifacts) == 0 {
		PrintProgress(osStdout, "No managed artifacts available for this task.")
		return 0
	}
	labels = append(labels, "Cancel")
	choice, err := prompt.choose(ctx, "Choose a managed artifact.", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(artifacts) {
		return 0
	}
	artifact := artifacts[choice]
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, latest, err := readJobRetryTarget(readCtx, client, job, run.ID, task.TaskIndex)
	cancel()
	if err != nil {
		return printErr("Could not recheck artifact", err)
	}
	current, err := jobresult.Validate(latest.OutputManifest)
	if err != nil {
		return printErr("Output manifest changed", err)
	}
	found := false
	for _, candidate := range current.Artifacts {
		if candidate == artifact {
			found = true
			break
		}
	}
	if latest.Attempt != task.Attempt || !found {
		return printErr("Artifact changed", errors.New("run the command again to review the task's current outputs"))
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "artifact-url", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), strconv.Itoa(task.TaskIndex), quoteLogCommandArg(artifact.Name))
	_, _ = fmt.Fprintln(osStdout, "Fresh-link command (POSIX shells):\n"+strings.Join(command, " "))
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := client.DownloadJobArtifact(readCtx, job.Name, run.ID, task.TaskIndex, artifact.Name)
	if err != nil {
		return printErr("Download request failed", err)
	}
	if out.Name != artifact.Name || out.SizeBytes != artifact.SizeBytes || out.SHA256 != artifact.SHA256 {
		return printErr("Artifact receipt changed", errors.New("download metadata does not match the selected artifact; inspect the task again"))
	}
	_, err = fmt.Fprintf(osStdout, "%s\n%s  %d bytes\n", out.Download.URL, out.SHA256, out.SizeBytes)
	if err != nil {
		return printErr("Output failed", err)
	}
	return 0
}
