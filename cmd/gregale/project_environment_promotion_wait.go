package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectEnvironmentPromotionPollInterval is a package variable so command
// tests can exercise a transition without sleeping for several seconds.
var projectEnvironmentPromotionPollInterval = 2 * time.Second

type projectEnvironmentPromotionWaitReceipt struct {
	Promotion     api.ProjectEnvironmentPromotionStatusResponse `json:"promotion"`
	Succeeded     bool                                          `json:"succeeded"`
	TimedOut      bool                                          `json:"timed_out,omitempty"`
	ResumeCommand string                                        `json:"resume_command,omitempty"`
	NextAction    string                                        `json:"next_action,omitempty"`
}

func waitForProjectEnvironmentPromotion(ctx context.Context, client *Client, projectSlug, targetEnvironment, promotionID string, timeout time.Duration, initial api.ProjectEnvironmentPromotionStatusResponse, onProgress func(api.ProjectEnvironmentPromotionStatusResponse)) (api.ProjectEnvironmentPromotionStatusResponse, bool, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	state := initial
	for {
		current, err := client.GetProjectEnvironmentPromotionStatus(waitCtx, projectSlug, targetEnvironment, promotionID)
		if err != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return state, true, nil
			}
			return state, false, err
		}
		state = current
		if onProgress != nil {
			onProgress(state)
		}
		if projectEnvironmentPromotionTerminal(state.Status) {
			return state, false, nil
		}

		timer := time.NewTimer(projectEnvironmentPromotionPollInterval)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return state, true, nil
			}
			return state, false, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func projectEnvironmentPromotionTerminal(status string) bool {
	return status == "succeeded" || status == "failed"
}

func projectEnvironmentPromotionProgressKey(status api.ProjectEnvironmentPromotionStatusResponse) string {
	var b strings.Builder
	b.WriteString(status.Status)
	b.WriteByte('|')
	b.WriteString(status.VerificationStatus)
	for _, workload := range status.Workloads {
		b.WriteByte('|')
		b.WriteString(workload.WorkloadSlug)
		b.WriteByte('=')
		b.WriteString(workload.Status)
		b.WriteByte('/')
		b.WriteString(workload.VerificationStatus)
	}
	return b.String()
}

func renderProjectEnvironmentPromotionProgress(status api.ProjectEnvironmentPromotionStatusResponse) {
	PrintProgress(osStdout, "Promotion %s: %s", status.PromotionID, status.Status)
	if status.VerificationStatus != "" {
		PrintProgress(osStdout, "  verification: %s", status.VerificationStatus)
	}
}

func projectEnvironmentPromotionStatusCommand(status api.ProjectEnvironmentPromotionStatusResponse) string {
	return fmt.Sprintf("gregale projects environments status %s %s --to %s", status.ProjectSlug, status.PromotionID, status.ToEnvironment)
}

func renderProjectEnvironmentPromotionWait(status api.ProjectEnvironmentPromotionStatusResponse, timedOut bool, timeout time.Duration) int {
	receipt := projectEnvironmentPromotionWaitReceipt{
		Promotion: status,
		Succeeded: status.Status == "succeeded",
	}
	if timedOut {
		receipt.TimedOut = true
		receipt.ResumeCommand = projectEnvironmentPromotionStatusCommand(status)
		receipt.NextAction = receipt.ResumeCommand
	}
	if status.Status == "failed" {
		receipt.NextAction = projectEnvironmentPromotionStatusCommand(status)
	}

	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		if timedOut {
			return 3
		}
		if !receipt.Succeeded {
			return 1
		}
		return 0
	}

	if timedOut {
		PrintWarn(osStderr, "promotion %s did not finish after %s; the server continues processing; resume with: %s", status.PromotionID, timeout, receipt.ResumeCommand)
		PrintProgress(osStderr, "next: %s", receipt.NextAction)
		return 3
	}
	renderProjectEnvironmentPromotionStatus(status)
	if !receipt.Succeeded {
		PrintProgress(osStderr, "next: %s", receipt.NextAction)
		return 1
	}
	return 0
}

func renderProjectEnvironmentPromotionStatus(status api.ProjectEnvironmentPromotionStatusResponse) {
	_, _ = fmt.Fprintf(osStdout, "Promotion %s: %s -> %s (%s)\n", status.PromotionID, status.FromEnvironment, status.ToEnvironment, status.Status)
	if status.Error != "" {
		_, _ = fmt.Fprintf(osStdout, "  error: %s\n", status.Error)
	}
	if status.VerificationStatus != "" {
		line := "  verification: " + status.VerificationStatus
		if status.VerificationError != "" {
			line += " — " + status.VerificationError
		}
		_, _ = fmt.Fprintln(osStdout, line)
	}
	for _, workload := range status.Workloads {
		line := fmt.Sprintf("  %-20s %s", workload.WorkloadSlug, workload.Status)
		if workload.VerificationStatus != "" {
			line += " (verification: " + workload.VerificationStatus + ")"
		}
		if workload.Error != "" {
			line += " — " + workload.Error
		}
		_, _ = fmt.Fprintln(osStdout, line)
	}
}
