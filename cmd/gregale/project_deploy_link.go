package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectDeployPlaceholderSlug is the app slug a project deploy carries
// through the single-app validation it shares with app deploys. A project
// deploy never targets that app, so a link that selects no single workload
// (the project-only link linkCheckoutAfterProjectDeploy writes) must not fail
// the deploy: the linked app wins, then the project slug, then the directory.
func projectDeployPlaceholderSlug(contextDir, project string) string {
	if app, err := linkedAppSlug(contextDir); err == nil && api.ValidAppSlug(app) {
		return app
	}
	if api.ValidAppSlug(project) {
		return project
	}
	if dir := sanitizeSlug(filepath.Base(contextDir)); api.ValidAppSlug(dir) {
		return dir
	}
	return "project"
}

// linkCheckoutAfterProjectDeploy records the deployed project in the
// checkout's .gregale/project.json when the checkout is not linked yet, so
// follow-up commands (deploy --project, status, logs --project) target the
// project just created. An existing link to another project is never
// replaced; the customer is told how to switch instead. Failures only warn:
// the deploy itself already succeeded.
func linkCheckoutAfterProjectDeploy(w io.Writer, sourceDir, projectSlug string, quiet bool) {
	if strings.TrimSpace(sourceDir) == "" || !api.ValidProjectSlug(projectSlug) {
		return
	}
	cwd, _ := os.Getwd()
	previous, path, err := linkedProjectContext(sourceDir)
	switch {
	case err == nil && previous.Project == projectSlug:
		return
	case err == nil:
		if !quiet {
			PrintWarn(w, "This checkout stays linked to project %s (%s); run `%s link %s` to target %s by default.", previous.Project, displayProjectContextPath(cwd, path), recoveryCLI(), projectSlug, projectSlug)
		}
		return
	case !errors.Is(err, errProjectContextNotFound):
		return
	}
	root, err := projectContextRoot(sourceDir)
	if err != nil {
		return
	}
	saved, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: projectSlug})
	if err != nil {
		if !quiet {
			PrintWarn(w, "Could not link this checkout to project %s: %v", projectSlug, err)
		}
		return
	}
	ignoreErr := ensureProjectContextIgnored(root)
	if quiet {
		return
	}
	PrintOK(w, "Linked this checkout to project %s (%s); `%s unlink` undoes it.", projectSlug, displayProjectContextPath(cwd, saved), recoveryCLI())
	if ignoreErr != nil {
		PrintWarn(w, "Could not add .gregale/ to .gitignore: %v", ignoreErr)
	}
}
