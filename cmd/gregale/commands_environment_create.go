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

// envCreate provides the branch-like shorthand inside a linked project:
// gregale env create staging --from production.
func envCreate(args []string) int {
	flags, positional := splitArgsForFlags(args, "protected", "share-resources", "plan", "deploy", "yes", "progress")
	fs := newFlagSet("env-create", flag.ContinueOnError)
	from := fs.String("from", "", "source environment to clone")
	project := fs.String("project", "", "project slug (defaults to linked project)")
	protected := fs.Bool("protected", false, "protect the new environment")
	shareResources := fs.Bool("share-resources", false, "explicitly share managed database and object-storage data with the source environment")
	planOnly := fs.Bool("plan", false, "show a read-only clone plan without creating the environment")
	deploy := fs.Bool("deploy", false, "clone the environment and promote the source live releases")
	yes := fs.Bool("yes", false, "confirm clone and deployment")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying clone/deployment operations")
	progress := fs.Bool("progress", false, "print promotion transitions while waiting (requires --deploy)")
	timeoutSeconds := fs.Int("timeout", defaultDeployWaitTimeoutSeconds, "maximum seconds to wait for deployment readiness")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale env create <environment> --from <environment> [--project <slug>] [--protected] [--share-resources] [--plan | --deploy [--yes] [--idempotency-key KEY] [--progress] [--timeout SECONDS]]", "env")
		return 1
	}
	if !api.ValidProjectEnvironmentSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) || positional[0] == *from {
		return printErr("Invalid environment", errors.New("target and --from must be different valid environment slugs"))
	}
	if *planOnly && *deploy {
		return printErr("Invalid clone options", errors.New("--plan and --deploy cannot be used together"))
	}
	if *progress && !*deploy {
		return printErr("Invalid wait options", errors.New("--progress requires --deploy"))
	}
	if *timeoutSeconds <= 0 || *timeoutSeconds > 24*60*60 {
		return printErr("Invalid wait timeout", errors.New("--timeout must be between 1 and 86400 seconds"))
	}
	if err := validateDeployIdempotencyKey(*idempotencyKey); err != nil {
		return printErr("Invalid idempotency key", err)
	}
	projectSlug, err := environmentProjectSlug(*project)
	if err != nil {
		return printErr("Could not resolve project", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *planOnly {
		plan, planErr := client.GetProjectEnvironmentClonePlan(context.Background(), projectSlug, *from, positional[0], *shareResources)
		if planErr != nil {
			return printErr("Clone plan failed", planErr)
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(plan)); code != 0 {
				return code
			}
		} else {
			renderEnvironmentClonePlan(plan)
		}
		if !plan.CanClone {
			return 1
		}
		return 0
	}
	if *deploy {
		plan, planErr := client.GetProjectEnvironmentClonePlan(context.Background(), projectSlug, *from, positional[0], *shareResources)
		if planErr != nil {
			return printErr("Clone preflight failed", planErr)
		}
		if !plan.CanClone || !plan.CanPromote {
			if jsonOutput {
				if code := jsonOut(writeJSON(plan)); code != 0 {
					return code
				}
			}
			reasons := append([]string{}, plan.BlockingReasons...)
			if !plan.CanPromote {
				reasons = append(reasons, "one or more source workloads have no live release to promote")
			}
			return printErr("Clone and deployment are blocked", errors.New(strings.Join(reasons, "; ")))
		}
		if !*yes {
			if jsonOutput {
				if code := jsonOut(writeJSON(plan)); code != 0 {
					return code
				}
				return printErr("Confirmation required", errors.New("clone and deployment require --yes in JSON mode"))
			}
			if !stdoutIsTTY() || !stdinIsTTY() {
				return printErr("Confirmation required", errors.New("clone and deployment require --yes when stdin or stdout is not a TTY"))
			}
			_, _ = fmt.Fprintf(osStdout, "Clone %s from %s and promote %d source workload release(s)? [y/N] ", positional[0], *from, plan.WorkloadCount)
			line, readErr := readConfirmationLine(osStdin)
			if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
				return printErr("Aborted by user", errors.New("environment clone and deployment were not confirmed"))
			}
		}
	}
	createCtx := context.Background()
	if key := strings.TrimSpace(*idempotencyKey); key != "" {
		createCtx = api.ContextWithIdempotencyKey(createCtx, deployOperationIdempotencyKey(key, "environment-clone-create"))
	}
	environment, err := client.CreateProjectEnvironment(createCtx, projectSlug, api.CreateProjectEnvironmentRequest{
		Slug: positional[0], Protected: protected, FromEnvironment: *from, ShareResources: *shareResources,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	if *deploy {
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "Created environment %s; promoting live releases from %s...\n", environment.Slug, *from)
		}
		promotionArgs := []string{
			projectSlug, "--from", *from, "--to", environment.Slug,
			"--yes", "--wait", "--timeout", fmt.Sprint(*timeoutSeconds),
		}
		if *progress {
			promotionArgs = append(promotionArgs, "--progress")
		}
		if key := strings.TrimSpace(*idempotencyKey); key != "" {
			promotionArgs = append(promotionArgs, "--idempotency-key", deployOperationIdempotencyKey(key, "environment-clone-promote"))
		}
		if code := cmdProjectsEnvironmentPromote(promotionArgs); code != 0 {
			_, _ = fmt.Fprintf(osStderr, "Environment %s was created, but release promotion did not complete. Resume with: gregale projects environments promote %s --from %s --to %s --yes --wait\n", environment.Slug, projectSlug, *from, environment.Slug)
			return code
		}
		return 0
	}
	return renderProjectEnvironment(environment)
}

func renderEnvironmentClonePlan(plan api.ProjectEnvironmentClonePlanResponse) {
	_, _ = fmt.Fprintf(osStdout, "Environment clone plan %s: %s -> %s\n  cloneable: %t\n  source releases ready for promotion: %t\n",
		plan.ProjectSlug, plan.FromEnvironment, plan.ToEnvironment, plan.CanClone, plan.CanPromote)
	for _, action := range plan.Actions {
		workload := ""
		if action.WorkloadSlug != "" {
			workload = action.WorkloadSlug + ": "
		}
		count := ""
		if action.Count > 0 {
			count = fmt.Sprintf(" (%d) ", action.Count)
		}
		_, _ = fmt.Fprintf(osStdout, "  %-22s %-14s %s%s\n", workload+action.Resource, action.Action, count, action.Reason)
	}
	for _, reason := range plan.BlockingReasons {
		_, _ = fmt.Fprintf(osStdout, "  BLOCKED: %s\n", reason)
	}
	for _, warning := range plan.Warnings {
		_, _ = fmt.Fprintf(osStdout, "  NOTE: %s\n", warning)
	}
}

func environmentProjectSlug(explicit string) (string, error) {
	if explicit != "" {
		if !api.ValidProjectSlug(explicit) {
			return "", fmt.Errorf("project %q is not a valid project slug", explicit)
		}
		return explicit, nil
	}
	linked, _, err := findProjectContext(".")
	if errors.Is(err, errProjectContextNotFound) {
		return "", errors.New("run `gregale link <project-slug>` or pass --project")
	}
	if err != nil {
		return "", err
	}
	return linked.Project, nil
}
