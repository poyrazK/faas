package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type projectRollbackSession struct {
	Version int `json:"version,omitempty"`
	api.ProjectRollbackOperation
}

type projectRollbackStep = api.ProjectRollbackStep

func projectRollbackSessionPath(client *api.Client, id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", errors.New("invalid project rollback operation ID")
	}
	dir, err := deploySessionDir(client)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "project-rollbacks", id+".json"), nil
}

func (p projectRollbackSession) resumeCommand() string {
	return fmt.Sprintf("gregale project rollback %s --resume %s", p.Project, p.ID)
}

func resumeProjectRollback(ctx context.Context, client *api.Client, slug, id string, timeout time.Duration) int {
	path, err := projectRollbackSessionPath(client, id)
	if err != nil {
		return printErr("Could not read project rollback", err)
	}
	lock, err := lockDeploySession(path)
	if err != nil {
		return printErr("Could not lock project rollback", err)
	}
	defer func() { _ = lock.Close() }()
	raw, err := os.ReadFile(path)
	if err != nil {
		return printErr("Could not read project rollback", err)
	}
	var plan projectRollbackSession
	if err := json.Unmarshal(raw, &plan); err != nil {
		return printErr("Could not decode project rollback", err)
	}
	if plan.ID != id || plan.Version != 1 || plan.Project != slug || len(plan.Steps) == 0 {
		return printErr("Could not resume project rollback", errors.New("operation does not match this project or has an unsupported format"))
	}
	project, err := client.GetProject(ctx, slug)
	if err != nil {
		return printErr("Could not verify project identity", err)
	}
	if project.ID == "" || project.ID != plan.ProjectID {
		return printErr("Could not resume project rollback", errors.New("project identity has changed"))
	}
	if err := checkProjectRollbackResume(ctx, client, project, plan.Steps); err != nil {
		return renderProjectRollback(plan, false, err)
	}
	if err := preflightProjectRollback(ctx, client, &plan); err != nil {
		return renderProjectRollback(plan, false, err)
	}
	return executeProjectRollback(ctx, client, &plan, path, timeout, true)
}

func checkProjectRollbackResume(ctx context.Context, client *api.Client, project api.ProjectResponse, steps []projectRollbackStep) error {
	attached := make(map[string]bool, len(project.Workloads))
	for _, workload := range project.Workloads {
		attached[workload.Slug] = workload.Status != "deleted"
	}
	for _, step := range steps {
		if step.Status == "skipped" {
			continue
		}
		if !attached[step.Slug] {
			return fmt.Errorf("%s is no longer part of the project", step.Slug)
		}
		current, err := readProjectRollbackCurrent(ctx, client, step.Slug)
		if err != nil {
			return fmt.Errorf("%s: %w", step.Slug, err)
		}
		if step.AppID == current.AppID && step.Scope == scopeOrDefault(current.Scope) {
			switch {
			case step.Status == "rolled_back" && sameBindingDeployment(current.ID, step.TargetID):
				continue
			case step.Status != "rolled_back" && sameBindingDeployment(current.ID, step.CurrentID) &&
				sameProjectRollbackServingPeriod(current.RolloutCompletedAt, step.CurrentServedAt):
				continue
			case step.Status == "in_progress" && step.AttemptID != "" && sameBindingDeployment(current.ID, step.TargetID):
				// Cutover may have completed while the client was disconnected.
				// Execution still requires the receipt from this attempt.
				continue
			}
		}
		return fmt.Errorf("%s: production changed since the project rollback was planned", step.Slug)
	}
	return nil
}

func sameProjectRollbackServingPeriod(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func executeProjectRollback(ctx context.Context, client *api.Client, plan *projectRollbackSession, path string, timeout time.Duration, resuming bool) int {
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Status == "skipped" || step.Status == "rolled_back" {
			continue
		}
		if step.AttemptID == "" || (resuming && step.Status == "failed" && step.ReceiptID != "") {
			step.AttemptID, step.ReceiptID = uuid.NewString(), ""
		}
		step.Status = "in_progress"
		if err := writePrivateDeployState(path, plan); err != nil {
			return renderProjectRollback(*plan, false, err)
		}
		if !jsonOutput {
			PrintProgress(osStdout, "%s: restoring v%d", step.Slug, step.To)
		}
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		err := followProjectRollbackStep(stepCtx, client, plan, step, path)
		cancel()
		if err != nil {
			return renderProjectRollback(*plan, false, fmt.Errorf("%s: %w", step.Slug, err))
		}
	}
	return renderProjectRollback(*plan, false, nil)
}

func followProjectRollbackStep(ctx context.Context, client *api.Client, plan *projectRollbackSession, step *projectRollbackStep, path string) error {
	pin := api.RollbackOperation{ID: step.ReceiptID, AppID: step.AppID, Scope: step.Scope, CurrentDeploymentID: step.CurrentID, TargetDeploymentID: step.TargetID}
	if pin.ID == "" {
		receipt, err := submitProjectRollbackStep(ctx, client, plan, *step)
		if err != nil {
			return err
		}
		pin.ID, pin.Service = receipt.ID, receipt.Service
		if !rollbackReceiptMatches(receipt, pin) || pin.ID == "" {
			return errors.New("server returned a different rollback operation or deployment pair")
		}
		step.ReceiptID = pin.ID
		if err := writePrivateDeployState(path, plan); err != nil {
			return err
		}
	} else {
		receipt, err := client.GetRollbackOperation(ctx, step.Slug, pin.ID)
		if err != nil {
			return err
		}
		pin.Service = receipt.Service
	}
	out, err := pollRollbackOperation(ctx, client, step.Slug, pin, true, 2*time.Second)
	if out.Status == "failed" {
		step.Status = "failed"
	} else if err == nil {
		step.Status, step.DeploymentID = "rolled_back", step.TargetID
	} else {
		return err
	}
	return errors.Join(err, writePrivateDeployState(path, plan))
}

func submitProjectRollbackStep(ctx context.Context, client *api.Client, plan *projectRollbackSession, step projectRollbackStep) (api.RollbackOperation, error) {
	var empty api.RollbackOperation
	ctx = api.ContextWithIdempotencyKey(ctx, step.AttemptID)
	exactAttempt := step.Scope == api.DefaultEnvScope && !plan.AcknowledgeReleases
	if exactAttempt {
		receipt, err := client.GetRollbackOperation(ctx, step.Slug, step.AttemptID)
		var problem *api.APIError
		if err == nil || !errors.As(err, &problem) || problem.Problem.Status != http.StatusNotFound {
			return receipt, err
		}
		ctx = api.ContextWithRollbackAttempt(ctx, strings.ReplaceAll(step.AttemptID, "-", ""))
	}
	dep, err := client.CheckedRollback(ctx, step.Slug, api.RollbackRequest{
		AcknowledgeReleases: plan.AcknowledgeReleases, ExpectedCurrentDeploymentID: &step.CurrentID, TargetDeploymentID: &step.TargetID,
	})
	if err != nil {
		if exactAttempt {
			if receipt, readErr := client.GetRollbackOperation(ctx, step.Slug, step.AttemptID); readErr == nil {
				return receipt, nil
			}
		}
		return empty, err
	}
	if dep.RollbackOperation == nil {
		return empty, errors.New("server returned no rollback receipt")
	}
	if exactAttempt && !sameBindingDeployment(dep.RollbackOperation.ID, step.AttemptID) {
		return empty, errors.New("server did not return the pinned rollback receipt")
	}
	return *dep.RollbackOperation, nil
}

func readProjectRollbackCurrent(ctx context.Context, client *api.Client, slug string) (api.DeploymentResponse, error) {
	page, err := client.ListAppDeployments(ctx, slug, "", 200)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	var current api.DeploymentResponse
	for _, dep := range page.Items {
		scope := scopeOrDefault(dep.Scope)
		if (scope != api.DefaultEnvScope && scope != "production") || dep.Status != "live" || dep.TrafficPercent == 0 {
			continue
		}
		if current.ID != "" || dep.TrafficPercent != 100 || dep.RolloutState != "complete" {
			return api.DeploymentResponse{}, errors.New("production does not have a single completed serving revision")
		}
		current = dep
	}
	return current, nil
}

// Check every remaining service before saving or executing any rollback step.
// An in-flight step resumes through its own committed receipt.
func preflightProjectRollback(ctx context.Context, client *api.Client, plan *projectRollbackSession) error {
	blocked := 0
	for i := range plan.Steps {
		step := &plan.Steps[i]
		switch step.Status {
		case "blocked":
			blocked++
		case "planned", "failed":
			warnings, err := client.PreflightCheckedRollback(ctx, step.Slug, step.CurrentID, step.TargetID, plan.AcknowledgeReleases)
			if err == nil {
				step.Warnings = warnings
				continue
			}
			step.Status, step.Reason = "blocked", err.Error()
			var apiErr *api.APIError
			if errors.As(err, &apiErr) {
				step.Problem = &apiErr.Problem
			}
			blocked++
		}
	}
	if blocked != 0 {
		return fmt.Errorf("rollback preflight blocked %d service(s)", blocked)
	}
	return nil
}

func renderProjectRollback(plan projectRollbackSession, dryRun bool, failure error) int {
	resumePreview := dryRun && plan.ID != ""
	if jsonOutput {
		out := struct {
			projectRollbackSession
			DryRun        bool   `json:"dry_run,omitempty"`
			ResumeCommand string `json:"resume_command,omitempty"`
			Error         string `json:"error,omitempty"`
		}{projectRollbackSession: plan, DryRun: dryRun}
		if resumePreview && plan.ResumePlanHash != "" {
			out.ResumeCommand = plan.resumeCommand() + " --plan-hash " + plan.ResumePlanHash
		} else if !resumePreview && plan.ID != "" && plan.UndoOperationID == "" && (plan.PauseRequestedAt == nil || plan.Status == "blocked") {
			out.ResumeCommand = plan.resumeCommand()
		}
		if failure != nil {
			out.Error = failure.Error()
		}
		if code := jsonOut(writeJSON(out)); code != 0 {
			return code
		}
	} else {
		if plan.AutoUndo != nil && plan.AutoUndo.Status == api.ProjectRollbackAutoUndoStarted && plan.UndoOperationID != "" {
			PrintProgress(osStdout, "Automatic undo %s accepted. Inspect with gregale project rollback %s --status %s", plan.UndoOperationID, plan.Project, plan.UndoOperationID)
		} else if plan.UndoOperationID != "" {
			PrintProgress(osStdout, "Undo operation %s", plan.UndoOperationID)
		}
		if plan.AutoUndo != nil {
			switch plan.AutoUndo.Status {
			case api.ProjectRollbackAutoUndoPending, api.ProjectRollbackAutoUndoReady:
				PrintProgress(osStdout, "Preparing to start automatic undo")
			case api.ProjectRollbackAutoUndoBlocked:
				if plan.AutoUndo.Problem != nil {
					PrintWarn(osStdout, "Automatic undo paused. %s", plan.AutoUndo.Problem.Detail)
				}
			}
		}
		if plan.PauseRequestedAt != nil {
			PrintProgress(osStdout, "Pause requested at %s", plan.PauseRequestedAt.UTC().Format(time.RFC3339))
		}
		if plan.Status == "running" {
			switch plan.Phase {
			case api.ProjectRollbackPreparing:
				if plan.PreparationDeadline != nil {
					PrintProgress(osStdout, "Preparing rollback targets until %s", plan.PreparationDeadline.UTC().Format(time.RFC3339))
				}
			case api.ProjectRollbackReleasing:
				PrintProgress(osStdout, "Releasing unused rollback preparations")
			}
		}
		for _, step := range plan.Steps {
			switch step.Status {
			case "skipped":
				PrintProgress(osStdout, "%s: %s", step.Slug, step.Reason)
			case "blocked", "failed":
				if dryRun && step.From > 0 && step.To > 0 {
					PrintWarn(osStdout, "%s blocked: %s (v%d -> v%d)", step.Slug, step.Reason, step.From, step.To)
				} else {
					PrintWarn(osStdout, "%s blocked: %s", step.Slug, step.Reason)
				}
			case "planned":
				if dryRun {
					PrintProgress(osStdout, "%s: v%d -> v%d", step.Slug, step.From, step.To)
				}
			case "in_progress":
				if plan.Phase == api.ProjectRollbackPreparing && step.PreparationStatus != "" {
					PrintProgress(osStdout, "%s rollback preparation is %s", step.Slug, step.PreparationStatus)
				} else {
					PrintProgress(osStdout, "%s: restoring v%d", step.Slug, step.To)
				}
			case "rolled_back":
				PrintOK(osStdout, "%s: restored v%d", step.Slug, step.To)
			}
			if step.Reason != "" && (step.Status == "in_progress" || step.Status == "rolled_back") {
				PrintWarn(osStdout, "%s: %s", step.Slug, step.Reason)
			}
			if health := step.PreCutoverHealth; health != nil {
				if dryRun && (!resumePreview || health.Status == "pending") {
					PrintProgress(osStdout, "%s will run its configured health probes before project traffic moves", step.Slug)
				} else {
					PrintProgress(osStdout, "%s pre-cutover health verification is %s", step.Slug, strings.ReplaceAll(health.Status, "_", " "))
				}
			}
			if health := step.Health; health != nil {
				switch {
				case dryRun && plan.StabilizeForSeconds > 0 && (!resumePreview || health.Status == "pending"):
					PrintProgress(osStdout, "%s will require %s of healthy probes after cutover", step.Slug, time.Duration(plan.StabilizeForSeconds)*time.Second)
				case resumePreview && step.Status == "rolled_back" && health.Status == "pending":
					PrintProgress(osStdout, "%s will rerun its configured health probes before continuing", step.Slug)
				case dryRun && health.Status == "pending":
					PrintProgress(osStdout, "%s will run its configured health probes after cutover", step.Slug)
				case health.Status == "stabilizing" && health.HealthySince != nil && health.CheckedAt != nil:
					PrintProgress(osStdout, "%s health verification is stabilizing (%s of %s verified)", step.Slug,
						health.CheckedAt.Sub(*health.HealthySince).Truncate(time.Second), time.Duration(plan.StabilizeForSeconds)*time.Second)
				default:
					PrintProgress(osStdout, "%s health verification is %s", step.Slug, strings.ReplaceAll(health.Status, "_", " "))
				}
			}
			if gate := step.LiveTraffic; gate != nil {
				if resumePreview && step.Status == "rolled_back" && gate.Status == "pending" {
					PrintProgress(osStdout, "%s will recheck its production 5xx budgets before continuing", step.Slug)
				} else if dryRun && (!resumePreview || gate.Status == "pending") {
					PrintProgress(osStdout, "%s will verify its configured production 5xx budgets after cutover", step.Slug)
				} else {
					PrintProgress(osStdout, "%s live-traffic verification is %s", step.Slug, strings.ReplaceAll(gate.Status, "_", " "))
				}
			}
			if traffic := step.Traffic; traffic != nil {
				PrintProgress(osStdout, "%s HTTP recovery is %s", step.Slug, strings.ReplaceAll(traffic.Status, "_", " "))
				for _, milestone := range []struct {
					label string
					at    *time.Time
				}{{"Routing committed", traffic.RoutingCommittedAt}, {"Gateways applied the route", traffic.GatewaysAppliedAt}, {"First successful response", traffic.FirstSuccessAt}} {
					if milestone.at != nil {
						PrintProgress(osStdout, "  %s at %s", milestone.label, milestone.at.UTC().Format(time.RFC3339Nano))
					}
				}
			}
			if hold := step.UndoArtifacts; hold != nil {
				if hold.ExpiresAt == nil {
					PrintProgress(osStdout, "%s undo artifacts held while the operation runs", step.Slug)
				} else if hold.ExpiresAt.After(time.Now()) {
					PrintProgress(osStdout, "%s undo artifacts held until %s", step.Slug, hold.ExpiresAt.UTC().Format(time.RFC3339))
				} else {
					PrintProgress(osStdout, "%s undo artifact hold expired at %s", step.Slug, hold.ExpiresAt.UTC().Format(time.RFC3339))
				}
			}
			showCrossedReleases := dryRun && (resumePreview || plan.UndoOf != "") && len(step.CrossedReleases) > 0
			if showCrossedReleases {
				for _, run := range step.CrossedReleases {
					PrintWarn(osStdout, "%s crosses release v%d (%s, started %s). Its changes will remain applied.", step.Slug, run.Revision, run.Status, run.StartedAt)
				}
			}
			for _, warning := range step.Warnings {
				if showCrossedReleases && warning.Code == api.CodeRollbackCrossesRelease {
					continue
				}
				PrintWarn(osStdout, "%s: %s", step.Slug, warning.Message)
			}
		}
		if plan.RecoveryMS != nil {
			PrintOK(osStdout, "Project HTTP recovery confirmed in %s", time.Duration(*plan.RecoveryMS)*time.Millisecond)
		}
		if dryRun && plan.UndoOnLiveTrafficFailure {
			PrintProgress(osStdout, "A sustained production 5xx breach will trigger automatic undo if reconfirmed")
		}
		if dryRun && plan.UndoOnHealthFailure {
			PrintProgress(osStdout, "A confirmed health probe 5xx after cutover will trigger automatic undo")
		}
		if resumePreview && plan.ResumePlanHash != "" {
			PrintProgress(osStdout, "Plan hash %s", plan.ResumePlanHash)
		} else if dryRun && plan.PlanHash != "" {
			PrintProgress(osStdout, "Plan hash %s", plan.PlanHash)
		}
		if failure == nil && !dryRun && plan.ID != "" {
			action := "rollback"
			if plan.UndoOf != "" {
				action = "rollback undo"
			}
			switch plan.Status {
			case "", "complete":
				PrintOK(osStdout, "Project %s %s complete", plan.Project, action)
			default:
				status := plan.Status
				if status == "blocked" && plan.PauseRequestedAt != nil {
					status = "paused"
				}
				PrintProgress(osStdout, "Project %s %s %s is %s", plan.Project, action, plan.ID, status)
			}
		}
	}
	if failure == nil {
		return 0
	}
	if jsonOutput {
		return 1
	}
	if plan.ID != "" && !resumePreview {
		if plan.PauseRequestedAt != nil && plan.Status == "running" {
			failure = fmt.Errorf("%w. Inspect with gregale project rollback %s --status %s", failure, plan.Project, plan.ID)
		} else {
			failure = fmt.Errorf("%w. Resume with %s", failure, plan.resumeCommand())
		}
	}
	return printErr("Project rollback stopped", failure)
}
