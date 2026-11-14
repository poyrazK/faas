package main

// adr: 20261031193635 — environment branches are dark: apid refuses them
// until environment_branches_enabled is on. Until they are released, the CLI
// keeps `environment branch` out of every customer surface: help,
// completions, man pages, the generated reference and dispatch. Setting
// FAAS_ENVIRONMENT_BRANCHES_ENABLED in the shell shows it.

import (
	"os"
	"strings"
)

func environmentBranchCLI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_ENVIRONMENT_BRANCHES_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// withoutEnvironmentBranches is command as the CLI shows it while branches
// are hidden. It never modifies the shared manifest.
func withoutEnvironmentBranches(command cliCommand) cliCommand {
	if command.Name != "projects" || command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		if sub.Name == "environments" && sub.Subcommands != nil {
			nested := make([]cliSub, 0, len(sub.Subcommands))
			for _, verb := range sub.Subcommands {
				if verb.Name != "branch" {
					nested = append(nested, verb)
				}
			}
			sub.Subcommands = nested
		}
		subs = append(subs, sub)
	}
	command.Subcommands = subs
	return command
}
