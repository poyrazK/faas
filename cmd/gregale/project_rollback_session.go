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
	Version             int                   `json:"version"`
	ID                  string                `json:"operation_id,omitempty"`
	Project             string                `json:"project"`
	ProjectID           string                `json:"project_id"`
	DeployGroupID       string                `json:"deploy_group_id,omitempty"`
	AcknowledgeReleases bool                  `json:"acknowledge_releases,omitempty"`
	Steps               []projectRollbackStep `json:"steps"`
}

type projectRollbackStep struct {
	Service         string     `json:"service"`
	Slug            string     `json:"slug"`
	Status          string     `json:"status"`
	From            int        `json:"from_revision,omitempty"`
	To              int        `json:"to_revision,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	AppID           string     `json:"app_id,omitempty"`
	Scope           string     `json:"scope,omitempty"`
	CurrentID       string     `json:"expected_current_deployment_id,omitempty"`
	CurrentServedAt *time.Time `json:"current_served_at,omitempty"`
	TargetID        string     `json:"target_deployment_id,omitempty"`
	DeploymentID    string     `json:"deployment_id,omitempty"`
	AttemptID       string     `json:"attempt_id,omitempty"`
	ReceiptID       string     `json:"rollback_operation_id,omitempty"`
}

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

func renderProjectRollback(plan projectRollbackSession, dryRun bool, failure error) int {
	if jsonOutput {
		out := struct {
			projectRollbackSession
			DryRun        bool   `json:"dry_run,omitempty"`
			ResumeCommand string `json:"resume_command,omitempty"`
			Error         string `json:"error,omitempty"`
		}{projectRollbackSession: plan, DryRun: dryRun}
		if plan.ID != "" {
			out.ResumeCommand = plan.resumeCommand()
		}
		if failure != nil {
			out.Error = failure.Error()
		}
		if code := jsonOut(writeJSON(out)); code != 0 {
			return code
		}
	} else {
		for _, step := range plan.Steps {
			switch step.Status {
			case "skipped":
				PrintProgress(osStdout, "%s: %s", step.Slug, step.Reason)
			case "planned":
				if dryRun {
					PrintProgress(osStdout, "%s: v%d -> v%d", step.Slug, step.From, step.To)
				}
			case "rolled_back":
				PrintOK(osStdout, "%s: restored v%d", step.Slug, step.To)
			}
		}
		if failure == nil && !dryRun && plan.ID != "" {
			PrintOK(osStdout, "Project %s rollback complete", plan.Project)
		}
	}
	if failure == nil {
		return 0
	}
	if jsonOutput {
		return 1
	}
	return printErr("Project rollback stopped", fmt.Errorf("%w. Resume with: %s", failure, plan.resumeCommand()))
}
