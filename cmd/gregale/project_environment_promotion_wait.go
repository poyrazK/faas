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
	if status.Health != nil {
		_, _ = fmt.Fprintf(&b, "|%s:%d:%s", status.Health.Phase, status.Health.Generation, status.Health.Verdict)
	}
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
	if h := status.Health; h != nil {
		if renderPromotionAdmission(h) {
			renderPromotionControl(status)
			return
		}
		PrintProgress(osStdout, "promotion %s: %s (destination health: %s, %s, %d healthy rounds)", status.PromotionID, status.Status, h.Phase, h.Verdict, h.HealthyRounds)
		renderPromotionWorker(h.Worker, h.WorkerHealth)
		for _, progress := range h.Workers {
			renderPromotionWorker(progress.Worker, progress.Health)
		}
		renderPromotionControl(status)
		if h.Migration != nil {
			PrintProgress(osStdout, "  migration %s: %s (task %s)", h.Migration.Slug, h.Migration.Status, h.Migration.TaskID)
		}
		return
	}
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
	if h := status.Health; h != nil {
		if !renderPromotionAdmission(h) {
			_, _ = fmt.Fprintf(osStdout, "  destination health: %s (%s, %d consecutive healthy rounds)\n", h.Phase, h.Verdict, h.HealthyRounds)
			_, _ = fmt.Fprintf(osStdout, "  health generation: %d; deadline: %s\n", h.Generation, h.Deadline.UTC().Format(time.RFC3339))
			if h.Migration != nil {
				renderPromotionMigration(h.Migration)
			}
		}
		renderPromotionControl(status)
		if h.Recovery != nil {
			renderPromotionRecovery(h.Recovery)
		}
		if h.Message != "" {
			_, _ = fmt.Fprintf(osStdout, "  health: %s\n", h.Message)
		}
	}
	if status.SyncConfig {
		label := "synced"
		if status.Health != nil && (status.ReleaseGraph == nil || status.ReleaseGraph.TargetReleaseSetID == "") {
			label = "sync requested"
		}
		if status.RollbackStatus == "rolled_back" {
			label = "restored"
		}
		_, _ = fmt.Fprintf(osStdout, "  non-secret configuration: %s\n", label)
	}
	if status.Error != "" {
		_, _ = fmt.Fprintf(osStdout, "  error: %s\n", status.Error)
	}
	if status.RollbackStatus != "" {
		_, _ = fmt.Fprintf(osStdout, "  rollback: %s\n", status.RollbackStatus)
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

func renderPromotionMigrationPolicy(m *api.ProjectPromotionMigrationPolicy) {
	_, _ = fmt.Fprintf(osStdout, "Destination migration: %s (command %s, backward-compatible). Database changes remain after code/config rollback.\n", m.Slug, m.CommandHash)
}

func renderPromotionMigration(m *api.ProjectReleaseCommand) {
	_, _ = fmt.Fprintf(osStdout, "  destination migration: %s (%s), task %s\n", m.Slug, m.Status, m.TaskID)
	if m.ExitCode != nil {
		_, _ = fmt.Fprintf(osStdout, "  migration exit: %d\n", *m.ExitCode)
	}
	if m.Status == "uncertain" {
		_, _ = fmt.Fprintln(osStdout, "  inspect the task and database before retrying; the command will not be replayed automatically")
	}
	_, _ = fmt.Fprintln(osStdout, "  database changes remain after code/config rollback")
}

func renderPromotionControl(status api.ProjectEnvironmentPromotionStatusResponse) {
	h := status.Health
	if h == nil {
		return
	}
	if h.Control != nil {
		_, _ = fmt.Fprintf(osStdout, "  operator %s: %s (request %s)\n", h.Control.Action, h.Control.Status, h.Control.Request.RequestID)
	}
	if actions := api.ProjectPromotionControlActions(h); len(actions) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  next: gregale projects environments %s %s %s --to %s --generation %d\n", actions[0], status.ProjectSlug, status.PromotionID, status.ToEnvironment, h.Generation)
	}
}

func renderPromotionAdmission(h *api.ProjectPromotionHealth) bool {
	a := h.Admission
	if a == nil || a.StartedAt != nil {
		return false
	}
	label := "waiting for capacity"
	if h.Phase != "queued" {
		label = h.Phase + " before starting"
	}
	_, _ = fmt.Fprintf(osStdout, "  promotion admission: %s (%s); wait deadline: %s; generation: %d\n", label, a.Reason, a.WaitDeadline.UTC().Format(time.RFC3339), h.Generation)
	return true
}
