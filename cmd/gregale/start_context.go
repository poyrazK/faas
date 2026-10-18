package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Resolve from the selected source, which can differ from the shell's cwd.
func startSourceContext(source string) (localProjectContext, string, error) {
	link, _, err := findProjectContext(source)
	if errors.Is(err, errProjectContextNotFound) {
		return localProjectContext{}, "", nil
	}
	if err != nil {
		return link, "", err
	}
	key := sha256.Sum256([]byte(link.Project + "\x00" + link.App + "\x00" + link.Environment))
	return link, hex.EncodeToString(key[:]), nil
}

func (r *startRunner) resolveStartContext() error {
	link, key, err := startSourceContext(r.session.SourcePath)
	if err != nil {
		return fmt.Errorf("read selected source context: %w", err)
	}
	if r.session.AppID != "" || r.session.DeploymentID != "" {
		if key != r.session.SourceContext {
			return errors.New("the selected source's linked target changed; restore its link to retry this launch, or start a new launch")
		}
		return r.verifyStartProject(r.ctx, r.client)
	}
	r.session.SourceContext = key
	r.session.Project, r.session.Environment = link.Project, link.Environment
	r.session.ProjectID, r.session.EnvironmentID = "", ""
	if key != "" && link.Project == "" {
		r.session.AppSlug = link.App
	}
	if link.Project != "" {
		project, err := r.client.GetProject(r.ctx, link.Project)
		if err != nil {
			return fmt.Errorf("verify linked project: %w", err)
		}
		if project.ID == "" || project.Slug != link.Project {
			return errors.New("project response did not identify the linked project")
		}
		r.session.ProjectID = project.ID
		app := link.App
		if app == "" {
			if len(project.Workloads) == 0 {
				return errors.New("the linked project has no workloads; link a deployed app before starting")
			}
			if len(project.Workloads) == 1 {
				app = project.Workloads[0].Slug
			} else {
				choices := make([]string, 0, len(project.Workloads)+1)
				for _, workload := range project.Workloads {
					choices = append(choices, workload.Slug)
				}
				choices = append(choices, "Finish")
				choice, err := r.prompt.choose(r.ctx, "Choose the linked project workload for this source.", choices, len(choices)-1)
				if err != nil {
					return err
				}
				if choice == len(project.Workloads) {
					return io.EOF
				}
				app = project.Workloads[choice].Slug
			}
		}
		if !api.ValidAppSlug(app) {
			return errors.New("linked project returned an invalid workload name")
		}
		r.session.AppSlug = app
		if err := r.verifyStartProjectResponse(r.ctx, r.client, project); err != nil {
			return err
		}
	}
	// A manifest must not silently move an unscoped launch to another scope.
	scope, err := manifestDeploymentScope(r.session.AppSlug, r.session.SourcePath, r.session.Environment)
	if err != nil {
		return err
	}
	if scope != "" && scope != scopeOrDefault(r.session.Environment) {
		return errors.New("manifest resources target another scope; link the intended project environment before starting")
	}
	return nil
}

func (r *startRunner) verifyStartProject(ctx context.Context, client *Client) error {
	if r.session.Project == "" {
		return nil
	}
	project, err := client.GetProject(ctx, r.session.Project)
	if err != nil {
		return fmt.Errorf("verify linked project: %w", err)
	}
	return r.verifyStartProjectResponse(ctx, client, project)
}

func (r *startRunner) verifyStartProjectResponse(ctx context.Context, client *Client, project api.ProjectResponse) error {
	if project.Slug != r.session.Project || project.ID == "" || (r.session.ProjectID != "" && project.ID != r.session.ProjectID) {
		return errors.New("linked project identity changed")
	}
	found := false
	for _, workload := range project.Workloads {
		found = found || workload.Slug == r.session.AppSlug
	}
	if !found {
		return fmt.Errorf("app %q is no longer a workload in linked project %q", r.session.AppSlug, r.session.Project)
	}
	r.session.ProjectID = project.ID
	if r.session.Environment != "" {
		environment, err := client.GetProjectEnvironment(ctx, r.session.Project, r.session.Environment)
		if err != nil {
			return fmt.Errorf("verify linked environment: %w", err)
		}
		if environment.ID == "" || environment.Slug != r.session.Environment || environment.ProjectID != project.ID || (r.session.EnvironmentID != "" && environment.ID != r.session.EnvironmentID) {
			return errors.New("linked environment identity changed")
		}
		r.session.EnvironmentID = environment.ID
	}
	return nil
}

func (r *startRunner) getLaunchApp(ctx context.Context, client *Client, slug string) (api.AppResponse, error) {
	if r.session.Environment != "" {
		return client.GetAppInEnvironment(ctx, slug, r.session.Environment)
	}
	return client.GetApp(ctx, slug)
}

func (r *startRunner) deploymentScopeMatches(dep api.DeploymentResponse) bool {
	return scopeOrDefault(dep.Scope) == scopeOrDefault(r.session.Environment)
}
