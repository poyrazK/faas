// cmd/gregale/commands_connect_repo.go — `gregale connect repo <owner>/<name>`.
//
// Issue #961 / Mega-B PR-1 — the CLI half of the trust-root decision
// in docs/adr/0XX-megab-trust-root.md. The CLI does NOT call
// POST /v1/apps/{slug}/install/bind. It opens the dashboard's
// /dashboard/apps/new?repo=<owner>/<name> URL in the browser, and
// the dashboard's cookie-session wizard handles the OAuth handshake
// + install + bind server-side (PR-3 wires the wizard).
//
// Why a separate file: the existing `cmdConnect` (commands2.go)
// handles a single service noun ("github"). Adding a positional
// + URL-shape verb widens the contract enough that a separate file
// keeps each handler ≤ 50 lines (CLAUDE.md handler cap).
//
// Why not overload `connect github`: the existing `connect github`
// subcommand is part of the documented public surface (CLI manifest
// `cli_meta.go:219`, docs URL). Customers run it from scripts.
// Overloading it would break those scripts the moment a customer's
// CI does `gregale connect github && gregale deploy --repo ...`.
// `connect repo` matches the noun (<owner>/<name> is a repo, not a
// GitHub account).
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/browser"
)

// cmdConnectRepo implements `gregale connect repo <owner>/<name>`.
// It opens the dashboard's new-app wizard pre-filled with the repo.
// The CLI does not block on the OAuth dance — the dashboard wizard
// drives the browser-side flow.
//
// On success the CLI exits 0. JSON mode emits
// {"url": "...", "service": "repo", "repo": "owner/name"} without
// opening the browser (mirrors the existing `connect github --json`
// shape so dashboards / automation can compose the verb).
func cmdConnectRepo(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale connect repo [--json] <owner>/<name>", "connect")
		return 1
	}
	repo := args[0]
	// validateRepoSlug (commands2.go) is the same predicate that
	// guards `gregale deploy --repo`. Sharing it keeps the shape
	// contract identical across the two surfaces so a deploy-shaped
	// repo is the only kind `connect repo` ever accepts.
	if err := validateRepoSlug(repo); err != nil {
		PrintFail(os.Stderr, "invalid repo %q: %v", repo, err)
		return 1
	}
	target := dashboardAppsNewURL(apiBase(), repo)
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"url":     target,
			"service": svcRepo,
			"repo":    repo,
		}))
	}
	fmt.Printf("Opening %s\n  to bind this repo to a Gregale app…\n", target)
	fmt.Fprintf(os.Stderr, "  (you'll be asked to authorize GitHub if you haven't already)\n")
	fmt.Fprintf(os.Stderr, "Next:\n  gregale deploy --repo %s --ref main\n", repo)
	if err := browser.Open(target); err != nil {
		// Mirror cmdConnect github: a soft failure — the URL is
		// the value the customer came for, missing the launch is
		// not worth a non-zero exit.
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
		return 0
	}
	return 0
}
