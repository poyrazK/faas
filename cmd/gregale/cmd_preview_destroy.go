// cmd/gregale/cmd_preview_destroy.go — `gregale preview destroy
// <slug>` (issue #961 Mega-C PR-1, leaf 3).
//
// Thin CLI wrapper around the new SDK method
// api.Client.DestroyPreview, which POSTs to
// /v1/preview/{slug}/destroy. Same posture as
// cmd_domains_verify.go: auth via the standard
// authedClient() helper, 10s timeout, friendly error on
// non-2xx.
//
// cmdPreview is the dispatcher for preview discovery and lifecycle commands.

package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// cmdPreview dispatches the `gregale preview` sub-commands.
func cmdPreview(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale preview <list|show|destroy> [args]", "preview")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdPreviewList(args[1:])
	case "show":
		return cmdPreviewShow(args[1:])
	case "destroy":
		return cmdPreviewDestroy(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown preview subcommand %q (try: list, show, destroy)\n", args[0])
		return 1
	}
}

// cmdPreviewDestroy is the `gregale preview destroy <slug>`
// handler. Returns 0 on success (204 No Content from apid),
// 1 on any error.
func cmdPreviewDestroy(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: gregale preview destroy <preview-slug>\n")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.DestroyPreview(ctx, slug); err != nil {
		// Surface the API problem verbatim (the dashboard's
		// preview-list page renders the same code, so the
		// customer can grep one identifier in two surfaces).
		return printErr("Destroy preview failed", err)
	}
	fmt.Printf("Preview %s torn down.\n", slug)
	return 0
}
