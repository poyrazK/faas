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

func cmdProjectsEnvironmentRoutes(args []string) int {
	if len(args) == 0 || args[0] != "set" {
		PrintUsage(os.Stderr, "usage: gregale projects environments routes set <project> <environment> <workload> (--file PATH|--stdin) [--yes]", "projects environments")
		return 1
	}
	flags, positional := splitArgsForFlags(args[1:], "file", "stdin", "yes")
	fs := newFlagSet("projects-environments-routes-set", flag.ContinueOnError)
	file := fs.String("file", "", "JSON route-policy file")
	stdin := fs.Bool("stdin", false, "read JSON route policy from stdin")
	yes := fs.Bool("yes", false, "confirm route-policy replacement")
	if err := fs.Parse(flags); err != nil || len(positional) != 3 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) || !api.ValidAppSlug(positional[2]) || (*file == "") == !*stdin {
		PrintUsage(os.Stderr, "usage: gregale projects environments routes set <project> <environment> <workload> (--file PATH|--stdin) [--yes]", "projects environments")
		return 1
	}
	if *stdin && !*yes {
		return printErr("Confirmation required", errors.New("--stdin requires --yes because the route policy consumes stdin"))
	}
	raw, err := readProjectEnvironmentConfigInput(*file, *stdin)
	if err != nil {
		return printErr("Could not read route policy", err)
	}
	var policy api.UpdateProjectEnvironmentRoutePolicyRequest
	if err := json.Unmarshal(raw, &policy); err != nil {
		return printErr("Invalid route policy JSON", err)
	}
	if policy.OnlyAllowDeclaredRoutes == nil || policy.DeclaredRoutes == nil {
		return printErr("Incomplete route policy", errors.New("only_allow_declared_routes and declared_routes are required"))
	}
	if !*yes {
		if jsonOutput || !stdoutIsTTY() || !stdinIsTTY() {
			return printErr("Confirmation required", errors.New("environment route update requires --yes when not interactive"))
		}
		_, _ = fmt.Fprintf(osStdout, "Replace routes for %s/%s/%s (enforced=%t, declarations=%d)? [y/N] ", positional[0], positional[1], positional[2], *policy.OnlyAllowDeclaredRoutes, len(*policy.DeclaredRoutes))
		line, readErr := readConfirmationLine(osStdin)
		if readErr != nil || (strings.ToLower(strings.TrimSpace(line)) != "y" && strings.ToLower(strings.TrimSpace(line)) != "yes") {
			return printErr("Aborted by user", errors.New("environment route update was not confirmed"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.UpdateProjectEnvironmentRoutes(context.Background(), positional[0], positional[1], positional[2], policy)
	if err != nil {
		return printErr("Could not update environment routes", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	_, _ = fmt.Fprintf(osStdout, "Updated %s/%s/%s routes: enforced=%t, declarations=%d\n", positional[0], positional[1], positional[2], updated.OnlyAllowDeclaredRoutes, len(updated.DeclaredRoutes))
	return 0
}
