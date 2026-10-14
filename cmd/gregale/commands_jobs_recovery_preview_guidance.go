package main

import (
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

type jobRecoveryPreviewStep struct {
	Reason  string `json:"reason"`
	Command string `json:"command"`
}

func jobRecoveryPreviewFollowArgs(options jobRecoveryPreviewOptions) []string {
	args := []string{}
	if options.CollectResults {
		args = append(args, "--results")
		if len(options.Include)+len(options.Exclude) == 0 {
			args = append(args, "--all-artifacts")
		}
		for _, pattern := range options.Include {
			args = append(args, "--include", pattern)
		}
		for _, pattern := range options.Exclude {
			args = append(args, "--exclude", pattern)
		}
	} else if options.Wait {
		args = append(args, "--wait")
	}
	if options.Wait {
		args = append(args, "--wait-timeout", options.WaitTimeout.String(), "--poll-interval", options.PollInterval.String())
	}
	return args
}

func blockedJobRetryNextSteps(name, runID string, task api.JobTaskResponse, options jobRecoveryPreviewOptions) []jobRecoveryPreviewStep {
	if !jobTaskRetryStatus(task.Status) {
		return []jobRecoveryPreviewStep{{"Inspect the current run and task outcomes before choosing another action.", savedNameSuggestionCommand([]string{"jobs", "status", name, runID})}}
	}
	args := append([]string{"jobs", "replay-failed", name, runID}, jobRecoveryPreviewFollowArgs(options)...)
	return []jobRecoveryPreviewStep{
		{"Inspect retained attempts for this task.", savedNameSuggestionCommand([]string{"jobs", "attempts", name, runID, strconv.Itoa(task.TaskIndex), "--all"})},
		{"Preview a linked replay of all unsuccessful inputs; this does not replay them and may itself be blocked.", savedNameSuggestionCommand(append(args, "--dry-run"))},
	}
}

func renderJobRecoveryPreviewSteps(steps []jobRecoveryPreviewStep) {
	if len(steps) == 0 {
		return
	}
	_, _ = fmt.Fprintln(osStdout, "Next steps (POSIX shells; inspection or preview only):")
	for _, step := range steps {
		_, _ = fmt.Fprintf(osStdout, "%s\n  %s\n", step.Reason, step.Command)
	}
}
