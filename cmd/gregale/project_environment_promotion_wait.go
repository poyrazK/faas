package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// projectEnvironmentPromotionPollInterval is a package variable so command
// tests can exercise a transition without sleeping for several seconds.
var projectEnvironmentPromotionPollInterval = 2 * time.Second

type projectEnvironmentPromotionWaitReceipt struct {
	Promotion            api.ProjectEnvironmentPromotionStatusResponse `json:"promotion"`
	Succeeded            bool                                          `json:"succeeded"`
	TimedOut             bool                                          `json:"timed_out,omitempty"`
	ResumeCommand        string                                        `json:"resume_command,omitempty"`
	VerificationCommands []string                                      `json:"verification_commands,omitempty"`
	NextAction           string                                        `json:"next_action,omitempty"`
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
		if err := validateBindingProjectPromotionReceipt(current, initial, projectSlug, targetEnvironment, promotionID); err != nil {
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
	if status.BindingsCheck != nil {
		raw, _ := json.Marshal(status.BindingsCheck.Blockers)
		b.Write(raw)
		b.WriteString(status.BindingsCheck.GraphDigest)
	}
	return b.String()
}

func renderProjectEnvironmentPromotionProgress(status api.ProjectEnvironmentPromotionStatusResponse) {
	PrintProgress(osStdout, "Promotion %s: %s", status.PromotionID, status.Status)
	if status.BindingsRequired && status.BindingsCheck != nil {
		renderProjectReleaseCheck(*status.BindingsCheck)
		renderPromotionVerificationCommands(status)
	}
	if status.VerificationStatus != "" {
		PrintProgress(osStdout, "  verification: %s", status.VerificationStatus)
	}
}

func projectEnvironmentPromotionStatusCommand(status api.ProjectEnvironmentPromotionStatusResponse) string {
	return fmt.Sprintf("gregale projects environments status %s %s --to %s", status.ProjectSlug, status.PromotionID, status.ToEnvironment)
}

func renderProjectEnvironmentPromotionWait(status api.ProjectEnvironmentPromotionStatusResponse, timedOut bool, timeout time.Duration) int {
	receipt := projectEnvironmentPromotionWaitReceipt{
		Promotion: status, VerificationCommands: promotionVerificationCommands(status),
		Succeeded: status.Status == "succeeded",
	}
	if timedOut {
		receipt.TimedOut = true
		receipt.ResumeCommand = projectEnvironmentPromotionStatusCommand(status) + " --wait --progress"
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
		if status.BindingsRequired {
			renderProjectEnvironmentPromotionStatus(status)
		}
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
	if status.BindingsRequired && status.BindingsCheck != nil {
		renderProjectReleaseCheck(*status.BindingsCheck)
		renderPromotionVerificationCommands(status)
	}
	if status.SyncConfig && status.Status == "succeeded" {
		_, _ = fmt.Fprintln(osStdout, "  non-secret configuration: synced")
	}
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

func validateBindingProjectPromotionReceipt(p, initial api.ProjectEnvironmentPromotionStatusResponse, project, environment, id string) error {
	if p.PromotionID != id || p.ProjectSlug != project || p.ToEnvironment != environment || initial.PromotionHash != "" && p.PromotionHash != initial.PromotionHash || initial.FromEnvironment != "" && p.FromEnvironment != initial.FromEnvironment || initial.BindingsRequired && !p.BindingsRequired {
		return fmt.Errorf("promotion status does not match the selected operation")
	}
	if p.Status != "running" && p.Status != "succeeded" && p.Status != "failed" {
		return fmt.Errorf("unknown promotion status %q", p.Status)
	}
	if !p.BindingsRequired || p.Status != "succeeded" {
		return nil
	}
	if p.ReleaseGraph == nil || p.ReleaseGraph.TargetReleaseSetID == "" || p.BindingsCheck == nil || !p.BindingsCheck.Passed {
		return fmt.Errorf("checked promotion omitted its activated graph or binding evidence")
	}
	deployments := map[string]string{}
	for _, w := range p.Workloads {
		if w.TargetDeploymentID == "" || deployments[w.WorkloadSlug] != "" {
			return fmt.Errorf("checked promotion omitted an exact target checkpoint")
		}
		deployments[w.WorkloadSlug] = canonicalGraphCLIUUID(w.TargetDeploymentID)
	}
	req := api.PublishProjectReleaseSetRequest{TTLSeconds: p.ReleaseGraph.TTLSeconds, ExpectedActiveReleaseID: &p.ReleaseGraph.PreviousTargetReleaseSetID, Deployments: deployments}
	return validateProjectReleaseCheck(*p.BindingsCheck, req, environment)
}

func promotionVerificationCommands(status api.ProjectEnvironmentPromotionStatusResponse) []string {
	report := status.BindingsCheck
	if report == nil || report.Passed {
		return nil
	}
	targets := map[string]string{}
	for _, w := range status.Workloads {
		targets[w.WorkloadSlug] = w.TargetDeploymentID
	}
	var commands []string
	for _, check := range report.Checks {
		_, deploymentErr := uuid.Parse(check.DeploymentID)
		if !api.ValidAppSlug(check.App) || deploymentErr != nil {
			continue
		}
		for _, binding := range check.Bindings {
			if binding.Status == "passed" || binding.Status == "disabled" {
				continue
			}
			base := "gregale bindings verify " + check.App
			switch binding.Type {
			case api.BindingTypeService:
				target := targets[binding.Name]
				_, targetErr := uuid.Parse(target)
				if !api.ValidAppSlug(binding.Name) || targetErr != nil {
					continue
				}
				base += " " + binding.Name + " --target-deployment " + canonicalGraphCLIUUID(target)
			case api.BindingTypePostgres:
				base += " --postgres " + promotionCommandArg(binding.Binding)
			case api.BindingTypeObjectStorage:
				base += " --object-storage " + promotionCommandArg(binding.Binding)
			default:
				continue
			}
			commands = append(commands, base+" --deployment "+canonicalGraphCLIUUID(check.DeploymentID))
		}
	}
	sort.Strings(commands)
	return commands
}
func renderPromotionVerificationCommands(status api.ProjectEnvironmentPromotionStatusResponse) {
	for _, command := range promotionVerificationCommands(status) {
		_, _ = fmt.Fprintf(osStdout, "  verify: %s\n", command)
	}
}

func promotionCommandArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
