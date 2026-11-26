package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// resolveLinkApp looks up the project an app belongs to for
// `gregale link --app <slug>`. standalone is true for an app outside any
// project. A non-zero code means the error was already printed.
func resolveLinkApp(slug, environment string) (project string, standalone bool, code int) {
	client, err := authedClient()
	if err != nil {
		return "", false, printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return "", false, printErr("Could not find app", err)
	}
	var projects []api.ProjectSummaryResponse
	if app.ProjectID != "" {
		if projects, err = client.ListProjects(ctx); err != nil {
			return "", false, printErr("Could not list projects", err)
		}
	}
	project, standalone, err = linkProjectForApp(app, projects, environment)
	if err != nil {
		return "", false, printErr("Could not link app", err)
	}
	return project, standalone, 0
}

// linkProjectForApp picks the link target for an app: its project's slug, or
// a standalone link when it has no project.
func linkProjectForApp(app api.AppResponse, projects []api.ProjectSummaryResponse, environment string) (string, bool, error) {
	if app.ProjectID == "" {
		if environment != "" {
			return "", false, fmt.Errorf("app %q is not in a project, so it has no environments; drop --environment", app.Slug)
		}
		return "", true, nil
	}
	for _, p := range projects {
		if p.ID == app.ProjectID {
			return p.Slug, false, nil
		}
	}
	return "", false, fmt.Errorf("app %q belongs to a project this account cannot list; run `gregale link <project> --app %s`", app.Slug, app.Slug)
}

// linkStandaloneApp saves an app outside any project as this directory's
// default, the same context init --deploy writes.
func linkStandaloneApp(slug string, noGitignore bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return printErr("Could not locate project context root", err)
	}
	nested := otherAppLinkedAbove(cwd, slug)
	if nested {
		root = cwd
	}
	previous, _, previousErr := linkedProjectContext(cwd)
	if previousErr != nil && !errors.Is(previousErr, errProjectContextNotFound) {
		return printErr("Could not read existing project context", previousErr)
	}
	linked := localProjectContext{Version: standaloneContextVersion, App: slug}
	path, err := saveProjectContext(root, linked)
	if err != nil {
		return printErr("Could not save project context", err)
	}
	if !noGitignore {
		if err := ensureProjectContextIgnored(root); err != nil {
			return printErr("App linked, but could not update .gitignore", err)
		}
	}
	receipt := projectContextReceipt{Context: linked, Path: displayProjectContextPath(cwd, path)}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	switch {
	case previousErr == nil && previous.Project != "":
		PrintProgress(osStdout, "Re-linked from project %s.", previous.Project)
	case previousErr == nil && previous.App != "" && previous.App != slug && !nested:
		PrintProgress(osStdout, "Re-linked from app %s.", previous.App)
	}
	PrintOK(osStdout, "Linked to app %s.", slug)
	renderProjectContext(osStdout, receipt)
	return 0
}
