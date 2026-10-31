package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261031193635 — environment branches.

const environmentBranchUsage = "usage: gregale environment branch <project-slug> <environment-slug> [--from <environment>] [--ttl <duration>]\n" +
	"       gregale environment branch <project-slug> --list [--from <environment>]"

// gregale environment branch <project> <name> --from production. The branch
// copies the parent's configuration and publishes each live workload from its
// existing build; nothing starts until a workload is called. Deleting it is
// `gregale environment delete`, and it is deleted at its TTL.
func cmdProjectsEnvironmentBranch(args []string) int {
	flags, positional := splitArgsForFlags(args, "list")
	fs := newFlagSet("projects-environments-branch", flag.ContinueOnError)
	from := fs.String("from", "production", "parent environment")
	ttl := fs.String("ttl", "", "time to live, such as 24h (default 168h, 1h to 720h)")
	list := fs.Bool("list", false, "list the parent's branches instead of creating one")
	if err := fs.Parse(flags); err != nil || len(positional) < 1 || len(positional) > 2 || *list == (len(positional) == 2) {
		PrintUsage(os.Stderr, environmentBranchUsage, "projects environments")
		return 1
	}
	if !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*from) {
		return printErr("Invalid environment", fmt.Errorf("project and environment slugs must use lowercase letters, numbers, and internal hyphens"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *list {
		branches, err := client.ListProjectEnvironmentBranches(context.Background(), positional[0], *from)
		if err != nil {
			return printErr("Could not list environment branches", err)
		}
		return renderProjectEnvironmentBranches(branches)
	}
	if !api.ValidProjectEnvironmentSlug(positional[1]) || positional[1] == *from {
		return printErr("Invalid environment", fmt.Errorf("the branch slug must be a valid environment slug that differs from --from"))
	}
	if _, err := api.ParseEnvironmentBranchTTL(*ttl); err != nil {
		return printErr("Invalid --ttl", err)
	}
	environment, err := client.CreateProjectEnvironmentBranch(context.Background(), positional[0], *from, api.CreateProjectEnvironmentBranchRequest{
		Slug: positional[1], TTL: *ttl,
	})
	if err != nil {
		return printErr("Could not branch environment", err)
	}
	return renderProjectEnvironmentBranch(environment)
}

func renderProjectEnvironmentBranch(environment api.ProjectEnvironmentResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(environment))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n", environment.Slug)
	if b := environment.Branch; b != nil {
		_, _ = fmt.Fprintf(osStdout, "  branched from: %s at %s\n  expires: %s\n  egress: %s\n", b.Parent, b.BranchPoint, b.ExpiresAt, b.Egress)
		if len(b.Workloads) == 0 {
			_, _ = fmt.Fprintln(osStdout, "  workloads: none live in the parent")
		}
		for _, w := range b.Workloads {
			_, _ = fmt.Fprintf(osStdout, "  %s: deployment %s from %s (starts on first request)\n", w.Workload, w.DeploymentID, w.SourceDeployment)
		}
	}
	return 0
}

func renderProjectEnvironmentBranches(list api.ProjectEnvironmentBranchListResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Branches) == 0 {
		_, _ = fmt.Fprintln(osStdout, "no branches")
		return 0
	}
	for _, b := range list.Branches {
		_, _ = fmt.Fprintf(osStdout, "%s  from %s  expires %s  depth %d\n", b.Slug, b.Parent, b.ExpiresAt, b.Depth)
	}
	return 0
}
