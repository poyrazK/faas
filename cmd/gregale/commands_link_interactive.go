package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"
)

func cmdLinkInteractive(projectSlug string, noGitignore bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return printErr("Could not locate project context root", err)
	}
	previous, previousPath, previousErr := linkedProjectContext(cwd)
	if previousErr != nil && !errors.Is(previousErr, errProjectContextNotFound) {
		return printErr("Could not read existing project context", previousErr)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	renderConnectionContext()
	if previousErr == nil {
		PrintProgress(osStdout, "Current checkout defaults:")
		renderProjectContext(osStdout, projectContextReceipt{Context: previous, Path: displayProjectContextPath(cwd, previousPath)})
		if previous.Environment == "" {
			PrintProgress(osStdout, "Environment: (no default)")
		}
	} else {
		PrintProgress(osStdout, "This checkout has no linked project.")
	}
	if projectSlug == "" {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		projects, err := client.ListProjects(readCtx)
		cancel()
		if err != nil {
			return printErr("Could not list projects", err)
		}
		if len(projects) == 0 {
			return printErr("No projects available", errors.New("create a project with gregale projects create before linking"))
		}
		sort.Slice(projects, func(i, j int) bool { return projects[i].Slug < projects[j].Slug })
		labels := make([]string, len(projects))
		fallback := 0
		for i, project := range projects {
			labels[i] = project.Slug
			if project.Slug == previous.Project {
				labels[i] += " (current)"
				fallback = i
			}
		}
		choice, err := prompt.choose(ctx, "Choose a project.", labels, fallback)
		if err != nil {
			return startInputExit(err)
		}
		projectSlug = projects[choice].Slug
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	project, err := client.GetProject(readCtx, projectSlug)
	cancel()
	if err != nil {
		return printErr("Could not read project", err)
	}
	sort.Slice(project.Workloads, func(i, j int) bool { return project.Workloads[i].Slug < project.Workloads[j].Slug })
	labels := []string{"No default app (choose an app when running commands)"}
	fallback := 0
	for i, app := range project.Workloads {
		label := app.Slug
		if previous.Project == projectSlug && previous.App == app.Slug {
			label += " (current)"
			fallback = i + 1
		}
		labels = append(labels, label)
	}
	appChoice, err := prompt.choose(ctx, "Choose the default app for "+projectSlug+".", labels, fallback)
	if err != nil {
		return startInputExit(err)
	}
	selected := localProjectContext{Version: projectContextVersion, Project: projectSlug}
	if appChoice > 0 {
		selected.App = project.Workloads[appChoice-1].Slug
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	environments, err := client.ListProjectEnvironments(readCtx, projectSlug)
	cancel()
	if err != nil {
		return printErr("Could not list environments", err)
	}
	sort.Slice(environments, func(i, j int) bool { return environments[i].Slug < environments[j].Slug })
	labels = []string{"No default environment (use each command's default scope)"}
	fallback = 0
	for i, environment := range environments {
		label := promotionEnvironmentLabel(environment)
		if previous.Project == projectSlug && previous.Environment == environment.Slug {
			label += " (current)"
			fallback = i + 1
		}
		labels = append(labels, label)
	}
	envChoice, err := prompt.choose(ctx, "Choose the default environment.", labels, fallback)
	if err != nil {
		return startInputExit(err)
	}
	if envChoice > 0 {
		selected.Environment = environments[envChoice-1].Slug
	}
	if err := selected.validate(); err != nil {
		return printErr("Invalid selected context", err)
	}
	PrintProgress(osStdout, "Proposed checkout defaults:")
	renderProjectContext(osStdout, projectContextReceipt{Context: selected, Path: displayProjectContextPath(cwd, projectContextPath(root))})
	if selected.Environment == "" {
		PrintProgress(osStdout, "Environment: (no default)")
	}
	if envChoice > 0 && environments[envChoice-1].Protected {
		PrintProgress(osStdout, "Environment %s is protected; its approval requirements apply to future operations.", selected.Environment)
	}
	if !noGitignore {
		PrintProgress(osStdout, "Add .gregale/ to the repository .gitignore if needed.")
	}
	confirmed, err := prompt.confirm(ctx, "Save these checkout defaults?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Checkout defaults were not changed.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	// Avoid overwriting a link changed while the user reviewed the choices.
	current, currentPath, currentErr := linkedProjectContext(cwd)
	if currentErr != nil && !errors.Is(currentErr, errProjectContextNotFound) {
		return printErr("Could not re-read project context", currentErr)
	}
	if current != previous || currentPath != previousPath || (currentErr == nil) != (previousErr == nil) {
		return printErr("Checkout defaults changed", errors.New("run link --interactive again to review the current defaults"))
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	latest, err := client.GetProject(readCtx, projectSlug)
	if err != nil {
		return printErr("Could not validate project", err)
	}
	if selected.App != "" {
		found := false
		for _, app := range latest.Workloads {
			if app.Slug == selected.App {
				found = true
				break
			}
		}
		if !found {
			return printErr("Project workloads changed", fmt.Errorf("app %s is no longer in project %s; run link again", selected.App, projectSlug))
		}
	}
	if selected.Environment != "" {
		environment, err := client.GetProjectEnvironment(readCtx, projectSlug, selected.Environment)
		if err != nil {
			return printErr("Could not validate environment", err)
		}
		if environment.Protected != environments[envChoice-1].Protected {
			return printErr("Environment protection changed", errors.New("run link again to review the updated environment"))
		}
	}
	return saveLinkContext(cwd, root, selected, previous, previousErr == nil, noGitignore, len(latest.Workloads))
}
