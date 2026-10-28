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

func waitAcceptedJobRun(client *api.Client, name string, accepted api.JobRunResponse, timeout, interval time.Duration) int {
	if !jobRunIDPattern.MatchString(accepted.ID) || accepted.JobID == "" || accepted.AccountID == "" {
		if jsonOutput {
			if code := jsonOut(writeJSONSingle(accepted)); code != 0 {
				return code
			}
		}
		return printErr("Cannot wait for accepted Job run", fmt.Errorf("acceptance response did not identify a complete run; inspect %s jobs runs %s before submitting again", recoveryCLI(), recoveryArgument(name)))
	}
	resume := recoveryCLI() + " job wait " + recoveryArgument(name) + " " + recoveryArgument(accepted.ID) + " --timeout " + timeout.String() + " --poll-interval " + interval.String()
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Job %s run accepted: %s. Waiting for this exact run.\nResume waiting (POSIX shells):\n  %s\n", name, accepted.ID, resume)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	job := api.JobResponse{Name: name, ID: accepted.JobID, AccountID: accepted.AccountID}
	result, code, err := followJobRun(ctx, client, job, accepted.ID, interval)
	if result.ID == "" {
		result = accepted
	}
	if jsonOutput {
		receipt := struct {
			Run           api.JobRunResponse `json:"run"`
			ExitCode      int                `json:"exit_code"`
			ResumeCommand string             `json:"resume_command"`
			Error         string             `json:"error,omitempty"`
		}{Run: result, ExitCode: code, ResumeCommand: resume}
		if err != nil {
			receipt.Error = err.Error()
		}
		if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
			return outputCode
		}
	} else if err != nil {
		printErr("Job wait ended", err)
		_, _ = fmt.Fprintf(osStderr, "Accepted run: %s (last observed status=%s). Continue waiting:\n  %s\n", result.ID, oneLine(result.AggregateStatus), resume)
	}
	return code
}
