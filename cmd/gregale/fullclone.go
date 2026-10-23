package main

// adr: 590 — full environment clones are not admitted yet: apid answers every
// full request with environment_full_clone_unavailable. Until they are, the
// CLI keeps `env create --full/--wait/--timeout` and `env clone-status` out of
// every customer surface: help, completions, man pages, the generated
// reference, dispatch and flag parsing. Setting
// FAAS_FULL_ENVIRONMENT_CLONES_ENABLED in the shell shows them.

import (
	"os"
	"slices"
	"strings"
)

func fullEnvironmentCloneCLI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_FULL_ENVIRONMENT_CLONES_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// fullEnvironmentCloneCreateFlags are the `env create` flags that only a full
// clone uses.
var fullEnvironmentCloneCreateFlags = []string{"full", "wait", "timeout"}

// withoutFullEnvironmentClone is command as the CLI shows it while full
// clones are hidden. It never modifies the shared manifest.
func withoutFullEnvironmentClone(command cliCommand) cliCommand {
	if command.Name != "env" || command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		switch sub.Name {
		case "clone-status":
			continue
		case "create":
			sub.Flags = slices.DeleteFunc(slices.Clone(sub.Flags), func(f cliFlag) bool {
				return slices.Contains(fullEnvironmentCloneCreateFlags, f.Name)
			})
			sub.Examples = []string{"gregale env create staging --from production"}
		}
		subs = append(subs, sub)
	}
	command.Subcommands = subs
	return command
}
