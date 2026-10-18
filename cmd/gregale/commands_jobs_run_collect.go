package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type jobRunCollectionReceipt struct {
	OutputDirectory string                 `json:"output_directory"`
	Include         []string               `json:"include,omitempty"`
	Exclude         []string               `json:"exclude,omitempty"`
	Run             api.JobRunResponse     `json:"run"`
	ExitCode        int                    `json:"exit_code"`
	Phase           string                 `json:"phase"`
	ResumeCommand   string                 `json:"resume_command,omitempty"`
	DownloadCommand string                 `json:"download_command,omitempty"`
	Artifacts       *jobRunArtifactReceipt `json:"artifacts,omitempty"`
	Error           string                 `json:"error,omitempty"`
}

func waitAndDownloadAcceptedJobRun(client *api.Client, name string, accepted api.JobRunResponse, timeout, interval time.Duration, directory string, includes, excludes []string) int {
	return waitAndCollectJobRun(context.Background(), client, name, accepted, timeout, interval, directory, false, includes, excludes)
}

func waitAndCollectJobRun(parentCtx context.Context, client *api.Client, name string, accepted api.JobRunResponse, timeout, interval time.Duration, directory string, resume bool, includes, excludes []string) int {
	receipt := jobRunCollectionReceipt{Include: includes, Exclude: excludes, Run: accepted, Phase: "wait", OutputDirectory: directory}
	if !jobRunIDPattern.MatchString(accepted.ID) || accepted.JobID == "" || accepted.AccountID == "" {
		receipt.ExitCode = 1
		receipt.Error = "the selected run lacks a complete identity; inspect run history before submitting again"
		receipt.ResumeCommand = savedNameSuggestionCommand([]string{"jobs", "runs", name})
		if jsonOutput {
			if code := jsonOut(writeJSONSingle(receipt)); code != 0 {
				return code
			}
		}
		return printErr("Cannot collect Job run", fmt.Errorf("%s; inspect %s", receipt.Error, receipt.ResumeCommand))
	}
	waitCommand := savedNameSuggestionCommand([]string{"jobs", "wait", name, accepted.ID, "--timeout", timeout.String(), "--poll-interval", interval.String(), "--out-dir", directory})
	downloadCommand := savedNameSuggestionCommand([]string{"jobs", "artifact-download", name, accepted.ID, "--all-tasks", "--out-dir", directory})
	statusCommand := savedNameSuggestionCommand([]string{"jobs", "status", name, accepted.ID})
	if resume {
		downloadCommand += " --resume"
	}
	waitCommand = appendJobArtifactFilters(waitCommand, includes, excludes)
	downloadCommand = appendJobArtifactFilters(downloadCommand, includes, excludes)
	selection := "accepted"
	if resume {
		selection = "selected"
	}
	receipt.DownloadCommand = downloadCommand
	receipt.ResumeCommand = waitCommand
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Job %s run %s: %s. Waiting for this exact run, then collecting artifacts.\nContinue waiting without resubmitting (POSIX shells):\n  %s\nCollect outputs after success (POSIX shells):\n  %s\n", name, selection, accepted.ID, waitCommand, downloadCommand)
	}
	signalCtx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	result, code, waitErr := followJobRun(ctx, client, api.JobResponse{Name: name, ID: accepted.JobID, AccountID: accepted.AccountID}, accepted.ID, interval)
	if result.ID != "" {
		receipt.Run = result
	}
	receipt.ExitCode = code
	if waitErr != nil {
		receipt.Error = waitErr.Error()
	}
	if code != 0 {
		if waitErr == nil {
			receipt.Error = "the run did not succeed; artifact collection was not started"
			receipt.ResumeCommand = statusCommand
		}
		if jsonOutput {
			if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
				return outputCode
			}
		} else {
			_, _ = fmt.Fprintf(osStderr, "Artifact collection not started. Selected run: %s; status: %s.\nNext command (POSIX shells):\n  %s\nCollect recorded outputs separately if needed:\n  %s\n", accepted.ID, oneLine(receipt.Run.AggregateStatus), receipt.ResumeCommand, downloadCommand)
			if waitErr != nil {
				printErr("Job wait ended", waitErr)
			}
		}
		return code
	}
	// A cancellation between phases must not begin artifact collection.
	if signalCtx.Err() != nil {
		receipt.ExitCode, receipt.Phase, receipt.Error = 130, "download", signalCtx.Err().Error()
		receipt.ResumeCommand = downloadCommand
		if !jsonOutput {
			printErr("Artifact collection interrupted", signalCtx.Err())
			_, _ = fmt.Fprintf(osStderr, "Collect this run without resubmitting (POSIX shells):\n  %s\n", downloadCommand)
		}
	} else {
		receipt.Phase = "download"
		if jsonOutput {
			receipt.ExitCode = downloadJobRunArtifactsWithContext(signalCtx, []string{name, accepted.ID}, directory, resume, false, includes, excludes, &accepted, func(artifacts jobRunArtifactReceipt) int { receipt.Artifacts = &artifacts; return 0 })
		} else {
			receipt.ExitCode = downloadJobRunArtifactsWithContext(signalCtx, []string{name, accepted.ID}, directory, resume, false, includes, excludes, &accepted, nil)
		}
		if receipt.ExitCode != 0 {
			receipt.Error = "artifact collection did not complete; no run was submitted during collection"
		}
		receipt.ResumeCommand = ""
		if receipt.ExitCode != 0 {
			receipt.ResumeCommand = downloadCommand
			if !resume {
				receipt.ResumeCommand += " --resume"
			}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Continue collecting this run without resubmitting (POSIX shells):\n  %s\n", receipt.ResumeCommand)
			}
		}
	}
	if jsonOutput {
		if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
			return outputCode
		}
	}
	return receipt.ExitCode
}
