package main

// adr: 20261019102657 — the realtime preview is rolled out per account.
// Until an operator opens it, the CLI keeps its verbs, the retained delivery
// mode and the gregale.yaml realtime section out of every customer surface:
// help, completions, man pages, the generated reference, command suggestions
// and dispatch. An account in the rollout sees them once apid's capability
// registry says so (realtime-channels not hidden); setting
// FAAS_REALTIME_PREVIEW_ACCOUNTS in the shell, the variable that also lists
// the rollout in apid, shows them everywhere, including the reference.

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
)

// realtimePreviewCapability is the capability that stands for the retained
// realtime preview (history, inbox, read progress, message edits, usage and
// the gregale.yaml realtime section). apid hides it outside the rollout.
const realtimePreviewCapability = "realtime-channels"

// realtimePreviewCLI is the static gate: the operator variable in the shell.
// The manifest the public reference and offline error paths read follows it
// alone, so they never depend on who is logged in or wait on the API.
func realtimePreviewCLI() bool {
	return strings.TrimSpace(os.Getenv("FAAS_REALTIME_PREVIEW_ACCOUNTS")) != ""
}

// realtimePreviewInView reports whether a capability view shows the realtime
// preview: the static gate, or an account view apid confirmed (GET
// /v1/capabilities) that does not hide it. Logged out, or with no confirmed
// view, the preview stays hidden (ADR-20261023115741 tier defaults).
// FAAS_CAPABILITY_FLAGS_RELEASE_ALL releases capability flags, not the
// account rollout, so a release-all view does not show the preview either.
func realtimePreviewInView(view capabilityView) bool {
	return realtimePreviewCLI() || !view.tier && !view.all && !view.hides(realtimePreviewCapability)
}

// realtimePreviewForAccount is realtimePreviewInView for the logged-in
// account: help, completions, and running the preview verbs, retained
// delivery and the gregale.yaml realtime section follow the account's
// rollout instead of needing the operator variable in the user's shell.
func realtimePreviewForAccount() bool {
	return realtimePreviewForAccountContext(context.Background())
}

// realtimePreviewForAccountContext is realtimePreviewForAccount for a caller
// holding a context, which bounds the account view read.
func realtimePreviewForAccountContext(ctx context.Context) bool {
	return realtimePreviewInView(discoveryCapabilityViewContext(ctx))
}

// realtimePreviewOffline is realtimePreviewInView for usage and error text:
// the run's view or the cached one, never a new API read (Hunt 14 H14-89).
func realtimePreviewOffline() bool {
	return realtimePreviewInView(offlineCapabilityView())
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
	return slices.Contains(realtimePreviewVerbs, verb) && !realtimePreviewForAccount()
}

func realtimeUsage() string {
	verbs := slices.Clone(realtimeVerbs)
	if !realtimePreviewOffline() {
		verbs = slices.DeleteFunc(verbs, func(verb string) bool { return slices.Contains(realtimePreviewVerbs, verb) })
	}
	return "usage: gregale realtime <" + strings.Join(verbs, "|") + ">"
}

func realtimePublishUsage() string {
	if realtimePreviewOffline() {
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
