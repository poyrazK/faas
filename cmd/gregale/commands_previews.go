// commands_previews.go — `gregale previews list|join|leave` (ADR
// 20261021080640). Customers join the platform previews an operator opened
// to self-serve, for their account or, with --org, for the whole
// organization (owner or admin).

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const previewsUsage = "usage: gregale previews list|join <key>|leave <key> [--org <slug>]"

func cmdPreviews(args []string) int {
	parent, _ := lookupCliCommand("previews")
	if len(args) == 0 {
		PrintUsage(os.Stderr, previewsUsage, "previews")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdPreviewsList(args[1:])
	case "join":
		return cmdPreviewsChange(args[1:], true)
	case "leave":
		return cmdPreviewsChange(args[1:], false)
	default:
		return printUnknownSubcommand(os.Stderr, "previews", parent, args[0])
	}
}

func cmdPreviewsList(args []string) int {
	fs := newFlagSet("previews list", flag.ContinueOnError)
	org := fs.String("org", "", "list the organization's previews (org slug)")
	if err := parseInterspersed(fs, args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var list api.PreviewList
	if slug := strings.TrimSpace(*org); slug != "" {
		list, err = client.ListOrgPreviews(context.Background(), slug)
	} else {
		list, err = client.ListPreviews(context.Background())
	}
	if err != nil {
		return printErr("List previews failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(list.Items))
	}
	if len(list.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no previews open to join)")
		return 0
	}
	for _, item := range list.Items {
		_, _ = fmt.Fprintf(osStdout, "%-24s %-8s %s\n", item.Key, previewState(item), item.Label)
	}
	return 0
}

// previewState is a one-word status for the list.
func previewState(item api.Preview) string {
	switch {
	case item.Joined:
		return "joined"
	case item.Enabled:
		return "on"
	case item.Eligible:
		return "open"
	default:
		return "closed"
	}
}

func cmdPreviewsChange(args []string, join bool) int {
	verb := map[bool]string{true: "join", false: "leave"}[join]
	fs := newFlagSet("previews "+verb, flag.ContinueOnError)
	org := fs.String("org", "", "act for the organization (org slug; owner or admin)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale previews "+verb+" <key> [--org <slug>]", "previews")
		return 1
	}
	key, slug := strings.TrimSpace(fs.Arg(0)), strings.TrimSpace(*org)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var item api.Preview
	switch {
	case join && slug != "":
		item, err = client.JoinOrgPreview(ctx, slug, key)
	case join:
		item, err = client.JoinPreview(ctx, key)
	case slug != "":
		item, err = client.LeaveOrgPreview(ctx, slug, key)
	default:
		item, err = client.LeavePreview(ctx, key)
	}
	if err != nil {
		return printErr(strings.ToUpper(verb[:1])+verb[1:]+" preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(item))
	}
	who := "You"
	if slug != "" {
		who = "Organization " + slug
	}
	if join {
		PrintOK(osStdout, "%s joined the %s preview (%s).", who, item.Label, onOff(item.Enabled))
	} else {
		PrintOK(osStdout, "%s left the %s preview (now %s).", who, item.Label, onOff(item.Enabled))
	}
	return 0
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
