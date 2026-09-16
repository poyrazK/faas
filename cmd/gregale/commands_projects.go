package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjects(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale projects <list|info|update|environments|rm>", "projects")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdProjectsList(args[1:])
	case "info":
		return cmdProjectsInfo(args[1:])
	case "update":
		return cmdProjectsUpdate(args[1:])
	case "environments", "envs":
		return cmdProjectsEnvironments(args[1:])
	case "rm", "delete":
		return cmdProjectsRemove(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown projects subcommand %q", args[0]), "projects")
		return 1
	}
}

func cmdProjectsEnvironments(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale projects environments <list|create|protect|unprotect|preview|promote>", "projects environments")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdProjectsEnvironmentsList(args[1:])
	case "create":
		return cmdProjectsEnvironmentCreate(args[1:])
	case "protect":
		return cmdProjectsEnvironmentProtection(args[1:], true)
	case "unprotect":
		return cmdProjectsEnvironmentProtection(args[1:], false)
	case "preview", "promotion-preview":
		return cmdProjectsEnvironmentPromotionPreview(args[1:])
	case "promote":
		return cmdProjectsEnvironmentPromote(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown project environments subcommand %q", args[0]), "projects environments")
		return 1
	}
}

func cmdProjectsEnvironmentPromote(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("projects-environments-promote", flag.ContinueOnError)
	from := fs.String("from", "", "source environment")
	to := fs.String("to", "", "target environment")
	yes := fs.Bool("yes", false, "confirm the promotion")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments promote <project-slug> --from <environment> --to <environment> [--yes]", "projects environments")
		return 1
	}
	if *from == *to {
		return printErr("Invalid environments", fmt.Errorf("--from and --to must be different"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetProjectEnvironmentPromotionPreview(context.Background(), positional[0], *to, *from)
	if err != nil {
		return printErr("Promotion preview failed", err)
	}
	if !preview.CanPromote {
		if jsonOutput {
			return jsonOut(writeJSON(preview))
		}
		return printErr("Promotion is blocked", errors.New(strings.Join(preview.BlockingReasons, "; ")))
	}
	if !*yes {
		if jsonOutput {
			if code := jsonOut(writeJSON(preview)); code != 0 {
				return code
			}
			return printErr("Confirmation required", errors.New("project environment promotion requires --yes in JSON mode"))
		}
		if stdoutIsTTY() && stdinIsTTY() {
			_, _ = fmt.Fprintf(osStdout, "Promote %s: %s -> %s (%d workload changes)? [y/N] ", preview.ProjectSlug, preview.FromEnvironment, preview.ToEnvironment, promotionChangeCount(preview))
			line, readErr := readConfirmationLine(osStdin)
			if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
				return printErr("Aborted by user", errors.New("promotion was not confirmed"))
			}
		} else {
			return printErr("Confirmation required", errors.New("project environment promotion requires --yes when stdin or stdout is not a TTY"))
		}
	}
	approvalToken := ""
	if preview.ApprovalRequired {
		approval, approvalErr := client.ApproveProjectEnvironment(context.Background(), positional[0], *to,
			api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken})
		if approvalErr != nil {
			return printErr("Protected environment approval failed", approvalErr)
		}
		approvalToken = approval.ApprovalToken
	}
	promoted, err := client.PromoteProjectEnvironment(context.Background(), positional[0], *to, api.PromoteProjectEnvironmentRequest{
		FromEnvironment: *from, PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		return printErr("Promotion failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(promoted))
	}
	_, _ = fmt.Fprintf(osStdout, "Promoted %s: %s -> %s\n", promoted.ProjectSlug, promoted.FromEnvironment, promoted.ToEnvironment)
	for _, workload := range promoted.Workloads {
		_, _ = fmt.Fprintf(osStdout, "  %-20s %s\n", workload.WorkloadSlug, workload.Status)
	}
	return 0
}

func promotionChangeCount(preview api.ProjectEnvironmentPromotionPreviewResponse) int {
	count := 0
	for _, change := range preview.Changes {
		if change.Kind != "unchanged" {
			count++
		}
	}
	return count
}

func cmdProjectsEnvironmentPromotionPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-preview", flag.ContinueOnError)
	from := fs.String("from", "", "source environment")
	to := fs.String("to", "", "target environment")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || !api.ValidProjectEnvironmentSlug(*to) {
		PrintUsage(os.Stderr, "usage: gregale projects environments preview <project-slug> --from <environment> --to <environment>", "projects environments")
		return 1
	}
	if *from == *to {
		return printErr("Invalid environments", fmt.Errorf("--from and --to must be different"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetProjectEnvironmentPromotionPreview(context.Background(), positional[0], *to, *from)
	if err != nil {
		return printErr("Promotion preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(preview))
	}
	_, _ = fmt.Fprintf(osStdout, "Promotion preview %s: %s -> %s\n  can promote: %t\n  approval required: %t\n  config changes: %d\n  promotion hash: %s\n",
		preview.ProjectSlug, preview.FromEnvironment, preview.ToEnvironment, preview.CanPromote,
		preview.ApprovalRequired, len(preview.ConfigDiff.Changes), preview.PromotionHash)
	for _, reason := range preview.BlockingReasons {
		_, _ = fmt.Fprintf(osStdout, "  blocked: %s\n", reason)
	}
	for _, change := range preview.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-16s %-10s %s\n", change.WorkloadSlug, change.Kind, change.SourceRevision)
	}
	return 0
}

func cmdProjectsEnvironmentsList(args []string) int {
	if len(args) != 1 || !api.ValidProjectSlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale projects environments list <project-slug>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environments, err := client.ListProjectEnvironments(context.Background(), args[0])
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(environments))
	}
	_, _ = fmt.Fprintf(osStdout, "%-24s %-12s %s\n", "SLUG", "PROTECTED", "UPDATED")
	for _, environment := range environments {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-12t %s\n", environment.Slug, environment.Protected, environment.UpdatedAt)
	}
	return 0
}

func cmdProjectsEnvironmentCreate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-create", flag.ContinueOnError)
	protected := fs.Bool("protected", false, "protect the environment from promotion")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 {
		PrintUsage(os.Stderr, "usage: gregale projects environments create <project-slug> <environment-slug> [--protected]", "projects environments")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) {
		return printErr("Invalid environment", fmt.Errorf("project and environment slugs must use lowercase letters, numbers, and internal hyphens"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environment, err := client.CreateProjectEnvironment(context.Background(), positional[0], api.CreateProjectEnvironmentRequest{
		Slug: positional[1], Protected: protected,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	return renderProjectEnvironment(environment)
}

func cmdProjectsEnvironmentProtection(args []string, protected bool) int {
	if len(args) != 2 || !api.ValidProjectSlug(args[0]) || !api.ValidProjectEnvironmentSlug(args[1]) {
		verb := "unprotect"
		if protected {
			verb = "protect"
		}
		PrintUsage(os.Stderr, "usage: gregale projects environments "+verb+" <project-slug> <environment-slug>", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	environment, err := client.UpdateProjectEnvironment(context.Background(), args[0], args[1], api.UpdateProjectEnvironmentRequest{Protected: &protected})
	if err != nil {
		return printErr("Update failed", err)
	}
	return renderProjectEnvironment(environment)
}

func renderProjectEnvironment(environment api.ProjectEnvironmentResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(environment))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n  protected: %t\n  updated: %s\n", environment.Slug, environment.Protected, environment.UpdatedAt)
	return 0
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
