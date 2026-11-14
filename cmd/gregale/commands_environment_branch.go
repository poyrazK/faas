package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261031193635 — environment branches.

const environmentBranchUsage = "usage: gregale environment branch <project-slug> <environment-slug> [--from <environment>] [--ttl <duration>] [--egress live|contained|simulated] [--private-networks off|join]\n" +
	"       gregale environment branch <project-slug> --list [--from <environment>]\n" +
	"       gregale environment branch <project-slug> <branch-slug> --status|--extend <duration>|--reset"

// gregale environment branch <project> <name> --from production. The branch
// copies the parent's configuration and publishes each live workload from its
// existing build; nothing starts until a workload is called. Deleting it is
// `gregale environment delete`, and it is deleted at its TTL.
func cmdProjectsEnvironmentBranch(args []string) int {
	flags, positional := splitArgsForFlags(args, "list", "status", "reset")
	fs := newFlagSet("projects-environments-branch", flag.ContinueOnError)
	from := fs.String("from", "production", "parent environment")
	ttl := fs.String("ttl", "", "time to live, such as 24h (default 168h, 1h to 720h)")
	list := fs.Bool("list", false, "list the parent's branches instead of creating one")
	status := fs.Bool("status", false, "show the branch: its parent, expiry, data, database copies and workloads")
	extend := fs.String("extend", "", "move the branch's expiry to this long from now, such as 48h (at most 30 days after its creation, 7 with database copies)")
	reset := fs.Bool("reset", false, "make the branch again from its parent with its settings; work done in it is discarded and its workloads get new URLs")
	egress := fs.String("egress", "", "outbound traffic: live (default), contained (only the branch's own services) or simulated (contained, and it can seed rehearsals)")
	privateNetworks := fs.String("private-networks", "", "off (default): the branch's workloads stay off their private networks; join: they join them with addresses of their own (live egress only)")
	if err := fs.Parse(flags); err != nil || !environmentBranchArgsValid(*list, *status, *reset, *extend != "", len(positional)) {
		PrintUsage(os.Stderr, environmentBranchUsage, "projects environments")
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
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
	if *status || *reset || *extend != "" {
		return environmentBranchLifecycle(client, positional[0], positional[1], *status, *reset, *extend)
	}
	if len(positional) != 2 {
		PrintUsage(os.Stderr, environmentBranchUsage, "projects environments")
		return 1
	}
	if !api.ValidProjectEnvironmentSlug(positional[1]) || positional[1] == *from {
		return printErr("Invalid environment", fmt.Errorf("the branch slug must be a valid environment slug that differs from --from"))
	}
	if _, err := api.ParseEnvironmentBranchTTL(*ttl); err != nil {
		return printErr("Invalid --ttl", err)
	}
	if flagName, err := validateEnvironmentBranchModes(*egress, *privateNetworks); err != nil {
		return printErr("Invalid "+flagName, err)
	}
	environment, err := client.CreateProjectEnvironmentBranch(context.Background(), positional[0], *from, api.CreateProjectEnvironmentBranchRequest{
		Slug: positional[1], TTL: *ttl, Egress: *egress, PrivateNetworks: *privateNetworks,
	})
	if err != nil {
		return printErr("Could not branch environment", err)
	}
	return renderProjectEnvironmentBranch(environment)
}

// environmentBranchArgsValid allows one mode: create (two slugs), --list
// (the project), or one of --status, --extend and --reset (project and
// branch).
func environmentBranchArgsValid(list, status, reset, extend bool, positional int) bool {
	modes := 0
	for _, on := range []bool{list, status, reset, extend} {
		if on {
			modes++
		}
	}
	switch {
	case modes > 1:
		return false
	case list:
		return positional == 1
	default:
		return positional == 2
	}
}

// environmentBranchLifecycle shows, extends or resets one branch.
func environmentBranchLifecycle(client *api.Client, project, branch string, status, reset bool, extend string) int {
	if !api.ValidProjectEnvironmentSlug(branch) {
		return printErr("Invalid environment", fmt.Errorf("the branch slug must be a valid environment slug"))
	}
	ctx := context.Background()
	switch {
	case status:
		environment, err := client.GetProjectEnvironmentBranch(ctx, project, branch)
		if err != nil {
			return printErr("Could not read environment branch", err)
		}
		return renderProjectEnvironmentBranch(environment)
	case reset:
		environment, err := client.ResetProjectEnvironmentBranch(ctx, project, branch)
		if err != nil {
			return printErr("Could not reset environment branch", err)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStdout, "reset from its parent; its workloads have new URLs")
		}
		return renderProjectEnvironmentBranch(environment)
	default:
		if _, err := api.ParseEnvironmentBranchTTL(extend); err != nil {
			return printErr("Invalid --extend", err)
		}
		environment, err := client.ExtendProjectEnvironmentBranch(ctx, project, branch, api.ExtendProjectEnvironmentBranchRequest{TTL: extend})
		if err != nil {
			return printErr("Could not extend environment branch", err)
		}
		return renderProjectEnvironmentBranch(environment)
	}
}

// validateEnvironmentBranchModes checks --egress and --private-networks and
// names the flag at fault.
func validateEnvironmentBranchModes(egress, privateNetworks string) (string, error) {
	switch egress {
	case "", api.EnvironmentBranchEgressLive, api.EnvironmentBranchEgressContained, api.EnvironmentBranchEgressSimulated:
	default:
		return "--egress", fmt.Errorf("--egress must be live, contained or simulated")
	}
	switch privateNetworks {
	case "", api.EnvironmentBranchPrivateNetworksOff:
	case api.EnvironmentBranchPrivateNetworksJoin:
		if egress != "" && egress != api.EnvironmentBranchEgressLive {
			return "--private-networks", fmt.Errorf("only a live branch can join private networks; a %s branch reaches only its own branch's services", egress)
		}
	default:
		return "--private-networks", fmt.Errorf("--private-networks must be off or join")
	}
	return "", nil
}

func renderProjectEnvironmentBranch(environment api.ProjectEnvironmentResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(environment))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\n", environment.Slug)
	if b := environment.Branch; b != nil {
		_, _ = fmt.Fprintf(osStdout, "  branched from: %s at %s\n  expires: %s\n  egress: %s\n", b.Parent, b.BranchPoint, b.ExpiresAt, b.Egress)
		if b.PrivateNetworks != "" {
			_, _ = fmt.Fprintf(osStdout, "  private networks: %s\n", b.PrivateNetworks)
		}
		_, _ = fmt.Fprintf(osStdout, "  data: %s\n", environmentBranchDataLine(b.Data, b.DataError))
		if len(b.Workloads) == 0 {
			_, _ = fmt.Fprintln(osStdout, "  workloads: none live in the parent")
		}
		for _, d := range b.Databases {
			copied := d.CopyDatabaseID
			if copied == "" {
				copied = "not made yet"
			}
			_, _ = fmt.Fprintf(osStdout, "  database %s: copy of %s (%s)\n", d.Name, d.SourceDatabaseID, copied)
		}
		for _, w := range b.Workloads {
			switch {
			case w.SourceDeployment != "":
				_, _ = fmt.Fprintf(osStdout, "  %s: deployment %s from %s (starts on first request)\n", w.Workload, w.DeploymentID, w.SourceDeployment)
			case w.DeploymentID != "":
				_, _ = fmt.Fprintf(osStdout, "  %s: deployment %s (%s)\n", w.Workload, w.DeploymentID, w.Status)
			default:
				_, _ = fmt.Fprintf(osStdout, "  %s: %s\n", w.Workload, w.Status)
			}
			if w.URL != "" {
				_, _ = fmt.Fprintf(osStdout, "    %s\n", w.URL)
			}
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
		_, _ = fmt.Fprintf(osStdout, "%s  from %s  expires %s  depth %d  data %s\n", b.Slug, b.Parent, b.ExpiresAt, b.Depth, environmentBranchDataLine(b.Data, b.DataError))
	}
	return 0
}

// environmentBranchDataLine explains a branch's data state in one phrase.
func environmentBranchDataLine(state, message string) string {
	switch state {
	case "pending":
		line := "pending (copying the parent's databases; workloads start once they exist)"
		if message != "" {
			line += ": " + message
		}
		return line
	case "failed":
		return "failed: " + message
	case "":
		return "ready"
	default:
		return state
	}
}
