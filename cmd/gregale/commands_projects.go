package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjects(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale projects <list|info|update|rm>", "projects")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdProjectsList(args[1:])
	case "info":
		return cmdProjectsInfo(args[1:])
	case "update":
		return cmdProjectsUpdate(args[1:])
	case "rm", "delete":
		return cmdProjectsRemove(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown projects subcommand %q", args[0]), "projects")
		return 1
	}
}

func cmdProjectsList(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale projects list", "projects")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	projects, err := client.ListProjects(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(projects))
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %-28s %-18s %s\n", "SLUG", "REPOSITORY", "BRANCH", "WORKLOADS")
	for _, project := range projects {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-28s %-18s %d\n", project.Slug, project.RepoFullName, project.ProductionBranch, project.WorkloadCount)
	}
	return 0
}

func cmdProjectsInfo(args []string) int {
	if len(args) != 1 || !api.ValidProjectSlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale projects info <slug>", "projects")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	project, err := client.GetProject(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	return renderProject(project)
}

func cmdProjectsUpdate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-update", flag.ContinueOnError)
	repo := fs.String("repo", "", "GitHub repository owner/name; empty unbinds")
	branch := fs.String("branch", "", "production branch")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale projects update <slug> [--repo owner/name] [--branch main]", "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if !seen["repo"] && !seen["branch"] {
		PrintUsage(os.Stderr, "projects update requires --repo or --branch", "projects")
		return 1
	}
	req := api.UpdateProjectRequest{}
	if seen["repo"] {
		req.RepoFullName = repo
	}
	if seen["branch"] {
		req.ProductionBranch = branch
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	project, err := client.UpdateProject(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Update failed", err)
	}
	return renderProject(project)
}

func cmdProjectsRemove(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes")
	fs := newFlagSet("projects-rm", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview affected state")
	yes := fs.Bool("yes", false, "confirm project deletion")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || (*dryRun == *yes) {
		PrintUsage(os.Stderr, "usage: gregale projects rm <slug> (--dry-run | --yes)", "projects")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) {
		return printErr("Invalid project slug", fmt.Errorf("%q does not match the project slug contract", positional[0]))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.PreviewDeleteProject(context.Background(), positional[0])
	if err != nil {
		return printErr("Preview failed", err)
	}
	if *dryRun {
		if jsonOutput {
			return jsonOut(writeJSON(preview))
		}
		_, _ = fmt.Fprintf(osStdout, "Project %s: %d workloads will be detached; %d domains, %d env values, and %d crons remain on those apps.\n",
			preview.Project.Slug, len(preview.Workloads), preview.DomainCount, preview.EnvCount, preview.CronCount)
		return 0
	}
	if err := client.DeleteProject(context.Background(), positional[0]); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"slug": positional[0], "deleted": true, "workloads_detached": len(preview.Workloads)}))
	}
	PrintOK(osStdout, "Deleted project %s; detached %d workloads", positional[0], len(preview.Workloads))
	return 0
}

func renderProject(project api.ProjectResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(project))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n  repository: %s\n  production branch: %s\n  scan source: %s\n  workloads: %d\n  exclusions: %s\n  last deploy: %s\n  last build: %s\n",
		project.Slug, project.RepoFullName, project.ProductionBranch, project.ScanSource,
		len(project.Workloads), strings.Join(project.Exclusions, ", "), project.LastReconciliationStatus, project.LastBuildStatus)
	return 0
}
