package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectsEnvironmentRoutingPolicies(args []string) int {
	const usage = "usage: gregale projects environments policies routing set <project> <environment> <workload> (--file PATH|--stdin) [--yes]"
	if len(args) == 0 || args[0] != "set" {
		PrintUsage(os.Stderr, usage, "projects environments")
		return 1
	}
	flags, positional := splitArgsForFlags(args[1:], "file", "stdin", "yes")
	fs := newFlagSet("projects-environments-policies-routing-set", flag.ContinueOnError)
	file := fs.String("file", "", "JSON redirect/rewrite policy file")
	stdin := fs.Bool("stdin", false, "read JSON policy from stdin")
	yes := fs.Bool("yes", false, "confirm policy replacement")
	if err := fs.Parse(flags); err != nil || len(positional) != 3 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) || !api.ValidAppSlug(positional[2]) || (*file == "") == !*stdin {
		PrintUsage(os.Stderr, usage, "projects environments")
		return 1
	}
	if *stdin && !*yes {
		return printErr("Confirmation required", errors.New("--stdin requires --yes because the policy consumes stdin"))
	}
	raw, err := readProjectEnvironmentConfigInput(*file, *stdin)
	if err != nil {
		return printErr("Could not read routing policy", err)
	}
	var policy api.UpdateProjectEnvironmentRoutingPolicyRequest
	if err := json.Unmarshal(raw, &policy); err != nil {
		return printErr("Invalid routing policy JSON", err)
	}
	if policy.Rules == nil {
		return printErr("Incomplete routing policy", errors.New("rules is required; use an empty list to disable inherited redirect/rewrite rules"))
	}
	if !*yes {
		if jsonOutput || !stdoutIsTTY() || !stdinIsTTY() {
			return printErr("Confirmation required", errors.New("environment routing policy update requires --yes when not interactive"))
		}
		_, _ = fmt.Fprintf(osStdout, "Replace redirect/rewrite rules for %s/%s/%s (%d rules)? [y/N] ", positional[0], positional[1], positional[2], len(*policy.Rules))
		line, readErr := readConfirmationLine(osStdin)
		if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
			return printErr("Aborted by user", errors.New("environment routing policy update was not confirmed"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.UpdateProjectEnvironmentRoutingPolicies(context.Background(), positional[0], positional[1], positional[2], policy)
	if err != nil {
		return printErr("Could not update environment routing policies", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	_, _ = fmt.Fprintf(osStdout, "Updated %s/%s/%s routing policies: %d redirect/rewrite rules\n", positional[0], positional[1], positional[2], len(updated.Rules))
	return 0
}
