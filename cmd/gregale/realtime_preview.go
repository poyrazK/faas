package main

// adr: 20261019102657 — the realtime preview is rolled out per account.
// Until an operator opens it, the CLI keeps its verbs, the retained delivery
// mode and the gregale.yaml realtime section out of every customer surface:
// help, completions, man pages, the generated reference, command suggestions
// and dispatch. Setting FAAS_REALTIME_PREVIEW_ACCOUNTS in the shell, the
// variable that also lists the rollout in apid, shows them.

import (
	"errors"
	"os"
	"slices"
	"strings"
)

func realtimePreviewCLI() bool {
	return strings.TrimSpace(os.Getenv("FAAS_REALTIME_PREVIEW_ACCOUNTS")) != ""
}

// realtimeVerbs are the realtime subcommands in usage order;
// realtimePreviewVerbs are the preview's.
var (
	realtimeVerbs = []string{"list", "usage", "get", "create", "update", "delete", "connections", "occupancy", "drain",
		"drain-status", "send", "send-principal", "edit-message", "delete-message", "receipt", "inbox", "push",
		"read-progress", "mark-read", "close", "subscribe", "unsubscribe", "publish", "auth"}
	realtimePreviewVerbs = []string{"usage", "occupancy", "edit-message", "delete-message", "inbox", "push",
		"read-progress", "mark-read"}
)

// hiddenRealtimeVerb reports whether verb is a preview realtime subcommand
// while the preview is hidden.
func hiddenRealtimeVerb(verb string) bool {
	return !realtimePreviewCLI() && slices.Contains(realtimePreviewVerbs, verb)
}

func realtimeUsage() string {
	verbs := slices.DeleteFunc(slices.Clone(realtimeVerbs), hiddenRealtimeVerb)
	return "usage: gregale realtime <" + strings.Join(verbs, "|") + ">"
}

func realtimePublishUsage() string {
	if realtimePreviewCLI() {
		return "usage: gregale realtime publish APP_SLUG ENDPOINT_ID CHANNEL (--data-stdin|--data DATA) [--binary] [--delivery live|retained] [--idempotency-key KEY]"
	}
	return "usage: gregale realtime publish APP_SLUG ENDPOINT_ID CHANNEL (--data-stdin|--data DATA) [--binary] [--delivery live] [--idempotency-key KEY]"
}

// errRealtimeManifestHidden is what a gregale.yaml realtime section meets
// while the preview is hidden: the strict decoder's unknown-key error.
var errRealtimeManifestHidden = errors.New(`gregale.yaml: unknown key "realtime"`)

// withoutRealtimePreview is command as the CLI shows it while the realtime
// preview is hidden. It never modifies the shared manifest.
func withoutRealtimePreview(command cliCommand) cliCommand {
	if command.Name != "realtime" || command.Subcommands == nil {
		return command
	}
	subs := make([]cliSub, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		if slices.Contains(realtimePreviewVerbs, sub.Name) {
			continue
		}
		if sub.Name == "publish" {
			flags := make([]cliFlag, len(sub.Flags))
			for i, f := range sub.Flags {
				switch f.Name {
				case "delivery":
					f.Short, f.ClosedSet = "live (the default)", []string{"live"}
				case "idempotency-key":
					f.Short = "stable retry key"
				}
				flags[i] = f
			}
			sub.Flags = flags
		}
		subs = append(subs, sub)
	}
	command.Subcommands = subs
	return command
}
