package main

// adr: 20261116213000 — custom Postgres roles are dark: apid refuses them
// until the postgres_custom_roles platform flag releases them. Until then the
// CLI keeps `postgres roles` out of every customer surface: help,
// completions, man pages, the generated reference and dispatch. Setting
// FAAS_POSTGRES_CUSTOM_ROLES_ENABLED in the shell shows it.

import (
	"os"
	"strings"
)

func postgresCustomRolesCLI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_POSTGRES_CUSTOM_ROLES_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// withoutPostgresCustomRoles is command as the CLI shows it while custom
// roles are hidden. It never modifies the shared manifest.
func withoutPostgresCustomRoles(command cliCommand) cliCommand {
	if command.Name != "postgres" || command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		if sub.Name != "roles" {
			subs = append(subs, sub)
		}
	}
	command.Subcommands = subs
	return command
}
