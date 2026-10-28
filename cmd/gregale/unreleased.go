package main

// adr: 1190 — application Auth is a dark launch. The CLI keeps its command,
// its flags on other commands (data-api create --auth, pipeline create
// --api/--auth, realtime create --auth, edge-rules --jwt-auth) and its template out of every customer
// surface: help, completions, man pages, the generated reference, command
// suggestions, dispatch and flag parsing. Setting FAAS_APPLICATION_AUTH_ENABLED
// in the shell, the variable that also turns Auth on in apid, shows them.

import (
	"flag"
	"os"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func applicationAuthCLI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_APPLICATION_AUTH_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// visibleCliCommands is the manifest every discovery surface reads: without
// unreleased Auth commands and flags, API monetization's verbs and flags
// (monetization.go) or the realtime preview's (realtime_preview.go), unless
// they are turned on.
func visibleCliCommands() []cliCommand {
	auth, monetization, realtimePreview := applicationAuthCLI(), monetizationCLI(), realtimePreviewCLI()
	if auth && monetization && realtimePreview {
		return cliCommands
	}
	visible := make([]cliCommand, 0, len(cliCommands))
	for _, command := range cliCommands {
		if !auth {
			if command.Unreleased {
				continue
			}
			command.Flags = releasedFlags(command.Flags)
			command.Subcommands = releasedSubcommands(command.Subcommands)
		}
		if !monetization {
			command = withoutMonetization(command)
		}
		if !realtimePreview {
			command = withoutRealtimePreview(command)
		}
		visible = append(visible, command)
	}
	return visible
}

func releasedSubcommands(subs []cliSub) []cliSub {
	if subs == nil {
		return nil
	}
	released := make([]cliSub, len(subs))
	for i, sub := range subs {
		sub.Flags = releasedFlags(sub.Flags)
		sub.Subcommands = releasedSubcommands(sub.Subcommands)
		released[i] = sub
	}
	return released
}

func releasedFlags(flags []cliFlag) []cliFlag {
	if flags == nil {
		return nil
	}
	released := make([]cliFlag, 0, len(flags))
	for _, f := range flags {
		if !f.Unreleased {
			released = append(released, f)
		}
	}
	return released
}

// unreleasedStringFlag registers an application Auth flag only while Auth
// is turned on; otherwise the flag does not exist.
func unreleasedStringFlag(fs *flag.FlagSet, name, usage string) *string {
	if applicationAuthCLI() {
		return fs.String(name, "", usage)
	}
	return new(string)
}

// templateAvailable reports whether a template name may be materialized by
// init and deploy: the Auth template only while Auth is turned on.
func templateAvailable(name string) bool {
	return templates.Exists(name) && (name != "auth" || applicationAuthCLI())
}

// dataAPICreateUsage names --auth only while application Auth is on.
func dataAPICreateUsage() string {
	if applicationAuthCLI() {
		return "usage: gregale data-api create NAME --database DATABASE (--auth AUTH_APP | --issuer HTTPS_URL --jwks-url HTTPS_URL --audience AUDIENCE) [--scope SCOPE] [--origins ORIGINS] [--public-read SCHEMA.RELATION[,...]] [--public-rpc SCHEMA.FUNCTION[,...]] [--aggregates] [--migrations DIR [--release-command COMMAND]]"
	}
	return "usage: gregale data-api create NAME --database DATABASE --issuer HTTPS_URL --jwks-url HTTPS_URL --audience AUDIENCE [--env SCOPE] [--origins ORIGINS] [--public-read SCHEMA.RELATION[,...]] [--public-rpc SCHEMA.FUNCTION[,...]] [--aggregates] [--migrations DIR [--release-command COMMAND]]"
}
