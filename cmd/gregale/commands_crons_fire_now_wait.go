package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type cronFireNowWaitReceipt struct {
	RequestID     string                       `json:"request_id"`
	WaitResult    string                       `json:"wait_result"`
	Request       *api.FireCronRequestResponse `json:"request,omitempty"`
	ResumeCommand string                       `json:"resume_command"`
	Error         string                       `json:"error,omitempty"`
}

// Following always GETs an existing receipt; it never fires or cancels a task.
func followCronFireNowRequest(parent context.Context, client *Client, cronID, requestID string, timeout time.Duration) int {
	signalCtx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	resume := cronRunCommand("fire-now", requestID) + " --wait --timeout " + quoteLogCommandArg(timeout.String())
	var latest *api.FireCronRequestResponse
	finish := func(result string, code int, err error) int {
		if jsonOutput {
			receipt := cronFireNowWaitReceipt{RequestID: requestID, WaitResult: result, Request: latest, ResumeCommand: resume}
			if err != nil {
				receipt.Error = err.Error()
			}
			if outputCode := jsonOut(writeJSON(receipt)); outputCode != 0 {
				return outputCode
			}
		} else {
			if err != nil {
				PrintWarn(osStdout, "%s", oneLine(err.Error()))
			}
			if result == "timeout" || result == "interrupted" || result == "error" {
				PrintProgress(osStdout, "Following stopped; the request may still continue. Resume with: %s", resume)
			}
		}
		return code
	}
	status := ""
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return finish("interrupted", 130, nil)
			}
			return finish("timeout", 3, nil)
		}
		progress, err := client.GetFireCronRequest(ctx, requestID)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			if latest == nil && isNotFound(err) {
				if cron, cronErr := client.GetCron(ctx, requestID); cronErr == nil && cron.ID != "" {
					err = fmt.Errorf("%s is a cron rule, not a fire-now request; submit it with gregale crons run %s", requestID, requestID)
				}
			}
			return finish("error", 1, err)
		}
		if !sameBindingDeployment(progress.RequestID, requestID) || !cronIDPattern.MatchString(progress.CronID) || cronID != "" && !sameBindingDeployment(progress.CronID, cronID) {
			return finish("error", 1, errors.New("the returned status does not match the selected task and request"))
		}
		if cronID == "" {
			cronID = progress.CronID
		}
		latest = &progress
		if progress.Status != status && !jsonOutput {
			renderFireNowStatus(osStdout, progress)
		}
		status = progress.Status
		switch status {
		case fireNowStatusSucceeded, fireNowStatusFailed, fireNowStatusCancelled:
			if !jsonOutput {
				if progress.OperationID != nil && *progress.OperationID != "" {
					PrintProgress(osStdout, "Operation: %s", oneLine(*progress.OperationID))
				}
				printCronHistoryCommand(cronID, "", "")
				if progress.TaskID != nil && fireNowRequestIDPattern.MatchString(*progress.TaskID) {
					printCronHistoryCommand(cronID, "", *progress.TaskID)
				}
				if status == fireNowStatusSucceeded {
					PrintProgress(osStdout, "Fire-now request completed; inspect the resulting run for execution details.")
				}
			}
			code := 1
			if status == fireNowStatusSucceeded {
				code = 0
			}
			return finish(status, code, nil)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
