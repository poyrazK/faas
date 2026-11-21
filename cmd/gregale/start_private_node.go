package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"gopkg.in/yaml.v3"
)

func privateNodeStarterName(dir string) (string, error) {
	data, _, err := readStartEditableFile(dir, filepath.Join(dir, "compose.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("x-gregale-starter")) {
		return "", nil
	}
	var compose struct {
		Name    string `yaml:"name"`
		Starter string `yaml:"x-gregale-starter"`
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return "", fmt.Errorf("read starter Compose config: %w", err)
	}
	if compose.Starter != templates.PrivateNode {
		return "", nil
	}
	if !api.ValidAppSlug(compose.Name) || api.IsReservedAppSlug(compose.Name) {
		return "", errors.New("private Node project needs a valid app name in compose.yaml")
	}
	return compose.Name, nil
}

func privateNodeDeployArgs(dir, slug string) []string {
	return []string{"--project", "--path", dir, "--source=worktree", "--name", slug, "--project-slug", slug, "--environment="}
}

func validatePrivateNodePlan(plan api.PlanResponse, slug, appID string) error {
	if plan.ProjectSlug != slug || plan.Environment != "" || len(plan.Workloads) != 1 || len(plan.Removed) != 0 {
		return errors.New("the private Node starter deploys exactly one app in its original project and default scope")
	}
	workload := plan.Workloads[0]
	if workload.Name != slug || (workload.AppSlug != "" && workload.AppSlug != slug) || workload.ExistingAppID != appID {
		return errors.New("the private Node plan targets a different app; use an available name for a new starter or restore the original project")
	}
	if workload.Class != "http" || workload.Visibility != api.AppVisibilityInternal || workload.PublicURL != "" {
		return errors.New("the starter must remain internal HTTP; restore its loopback port mapping in compose.yaml")
	}
	return nil
}

func verifyUnusedPrivateNodeProject(ctx context.Context, client *Client, slug string) error {
	_, err := client.GetProject(ctx, slug)
	if err == nil {
		return errors.New("this project name is already in use; choose an unused name for the private Node starter")
	}
	var problem *APIError
	if errors.As(err, &problem) && problem.Problem.Status == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("check project name: %w", err)
}

func (r *startRunner) selectPrivateNodeSource(dir, slug string) {
	r.session.SourcePath, r.session.Template = dir, templates.PrivateNode
	r.session.AppSlug, r.session.Project = slug, slug
}

func (r *startRunner) verifyPrivateNodeTarget(ctx context.Context, client *Client) error {
	project, err := client.GetProject(ctx, r.session.Project)
	if err != nil {
		return err
	}
	if project.ID == "" || project.ID != r.session.ProjectID || project.Slug != r.session.Project ||
		len(project.Workloads) != 1 || project.Workloads[0].Slug != r.session.AppSlug {
		return errors.New("the original private Node project or its workload changed")
	}
	app, err := client.GetApp(ctx, r.session.AppSlug)
	if err != nil {
		return err
	}
	if app.ID != r.session.AppID || app.ProjectID != r.session.ProjectID || app.Visibility != string(api.AppVisibilityInternal) {
		return errors.New("the original private Node app or its internal access has changed")
	}
	return nil
}

func (r *startRunner) reviewPrivateNodePlan(ctx context.Context, client *Client, plan api.PlanResponse) error {
	account, err := client.Whoami(ctx)
	if err != nil {
		return err
	}
	if account.ID != r.session.AccountID || client.BaseURL() != r.client.BaseURL() {
		return errors.New("account or API changed; start again to review the current connection")
	}
	r.account = account
	if err := r.requireStartCompatibility(ctx, client); err != nil {
		return err
	}
	link, key, err := startSourceContext(r.session.SourcePath)
	if err != nil {
		return err
	}
	if r.session.AppID == "" && key != "" && link.Project == r.session.Project && link.App == r.session.AppSlug && link.Environment == "" {
		project, err := client.GetProject(ctx, r.session.Project)
		if err != nil {
			return err
		}
		if len(project.Workloads) != 1 || project.Workloads[0].Slug != r.session.AppSlug {
			return errors.New("the linked project does not contain the original private Node app")
		}
		app, err := client.GetApp(ctx, r.session.AppSlug)
		if err != nil {
			return err
		}
		r.session.ProjectID, r.session.AppID = project.ID, app.ID
		r.session.SourceContext = key
	}
	if key != r.session.SourceContext {
		return errors.New("the selected source's linked target changed; restore its link before starting")
	}
	if err := validatePrivateNodePlan(plan, r.session.AppSlug, r.session.AppID); err != nil {
		return err
	}
	if r.session.AppID == "" {
		return verifyUnusedPrivateNodeProject(ctx, client, r.session.Project)
	}
	return r.verifyPrivateNodeTarget(ctx, client)
}

func (r *startRunner) confirmPrivateNodePlan(plan api.PlanResponse) (bool, error) {
	_, _ = fmt.Fprintf(osStdout, "\nReady to launch %s\n  Source   %s\n  Account  %s (%s plan)\n  Access   Internal only\n",
		r.session.AppSlug, r.session.SourcePath, r.account.Email, r.account.Plan)
	if r.session.ProjectID != "" {
		_, _ = fmt.Fprintf(osStdout, "  Project  Reuse %s (%s)\n", r.session.Project, r.session.ProjectID)
	}
	_, _ = fmt.Fprintln(osStdout, "  Check    Gregale verifies the deployment response through your account.")
	plan = withFeatureHints(plan)
	if r.account.Plan == string(api.PlanFree) {
		// The validated starter plan expects loopback-only ingress. Show it as
		// internal access above and keep any other scan warnings visible.
		loopbackWarning := "reposcan: compose.yaml: " + r.session.AppSlug + ": " + reposcan.ComposeLoopbackPortWarning
		plan.Warnings = slices.DeleteFunc(slices.Clone(plan.Warnings), func(warning string) bool {
			return warning == loopbackWarning
		})
		plan.FeatureHints = slices.DeleteFunc(slices.Clone(plan.FeatureHints), func(hint api.FeatureHint) bool {
			return hint.ID == featurehints.IDHealthDefault
		})
	}
	printPreparedPlanText(osStdout, plan, nil, false, false)
	return r.prompt.confirm(r.ctx, "Deploy this app")
}

func (r *startRunner) deployPrivateNode() (int, bool) {
	if r.session.ProjectSessionID != "" {
		return printErr("A project submission needs recovery", errors.New("run gregale start again to check the accepted release")), false
	}
	PrintProgress(osStdout, "3/4 · Review the private Node project")
	args := append(privateNodeDeployArgs(r.session.SourcePath, r.session.Project),
		"--no-wait", "--idempotency-key", uuid.NewString())
	submitted := false
	code := r.deploy(r.ctx, args, false, deployExecution{
		projectPlan:     r.reviewPrivateNodePlan,
		confirmProject:  r.confirmPrivateNodePlan,
		skipProjectLink: true,
		onProjectAttempt: func(id string) error {
			next := r.session
			next.ProjectSessionID = id
			if err := saveStartSession(r.statePath, next); err != nil {
				return err
			}
			r.session = next
			PrintProgress(osStdout, "4/4 · Build, boot and verify readiness")
			return nil
		},
		onProjectApply: func(apply api.ApplyResponse) error {
			var err error
			submitted, err = r.acceptPrivateNodeApply(apply)
			return err
		},
	})
	if code != 0 {
		r.printRecovery()
		return code, false
	}
	if !submitted {
		return code, false
	}
	return r.resume(), true
}

func (r *startRunner) acceptPrivateNodeApply(apply api.ApplyResponse) (bool, error) {
	next := r.session
	if apply.ProjectID == "" || (next.ProjectID != "" && apply.ProjectID != next.ProjectID) ||
		len(apply.Apps) != 1 || apply.Apps[0].Slug != next.AppSlug || apply.Apps[0].ID == "" ||
		(next.AppID != "" && next.AppID != apply.Apps[0].ID) {
		return false, errors.New("project submission receipt did not match the reviewed app")
	}
	if len(apply.Builds) == 0 && next.AppID != "" {
		next.ProjectSessionID = ""
		if err := saveStartSession(r.statePath, next); err != nil {
			return false, err
		}
		r.session = next
		return false, nil
	}
	if len(apply.Builds) != 1 || apply.Builds[0].Slug != next.AppSlug || apply.Builds[0].AppID != apply.Apps[0].ID || apply.Builds[0].DeploymentID == "" {
		return false, errors.New("the private Node project did not accept an app deployment; inspect its project receipt")
	}
	next.ProjectSessionID = ""
	next.ProjectID, next.AppID = apply.ProjectID, apply.Apps[0].ID
	next.DeploymentID, next.Status = apply.Builds[0].DeploymentID, "queued"
	next.RequireDeploymentEvidence = true
	if err := saveStartSession(r.statePath, next); err != nil {
		return false, err
	}
	r.session, r.lastRequest = next, nil
	return true, nil
}

func (r *startRunner) recoverPrivateNodeApply() error {
	ctx, cancel := context.WithTimeout(r.ctx, r.waitTimeout)
	defer cancel()
	path, err := selectDeploySession(r.client, []string{r.session.ProjectSessionID}, "")
	if err != nil {
		return err
	}
	lock, err := lockDeploySession(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	saved, err := loadSavedDeploySession(r.client, path)
	if err != nil {
		return err
	}
	if saved.record.Project == nil || saved.record.Project.Slug != r.session.Project || saved.record.Project.Environment != "" || saved.record.Project.Refused {
		return errors.New("project submission does not match the saved private Node launch")
	}
	if saved.record.Project.AttemptID != "" && saved.record.Outcome != deploySubmissionAccepted {
		if err := saved.discoverProjectSubmission(ctx, r.client); err != nil {
			return err
		}
	}
	_, err = r.acceptPrivateNodeApply(saved.projectApplyReceipt())
	return err
}
