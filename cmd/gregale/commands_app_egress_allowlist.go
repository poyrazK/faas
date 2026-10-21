package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const subEgressAllowlist = "egress-allowlist"

// cmdAppEgressAllowlist is the C1 remediation action. The server remains the
// source of truth for EgressAllowlistAllowed, CIDR validation, and quota; the
// CLI only performs a read-modify-write against the existing app PATCH API.
func cmdAppEgressAllowlist(slug string, args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> egress-allowlist {show|add <cidr>|remove <cidr>|clear}", "apps")
		return 1
	}
	action := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not load app", err)
	}
	current := append([]string(nil), app.EgressAllowlist...)
	before := append([]string(nil), current...)
	switch action {
	case "show":
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Slug      string   `json:"slug"`
				Allowlist []string `json:"egress_allowlist"`
			}{Slug: slug, Allowlist: current}))
		}
		if len(current) == 0 {
			_, _ = fmt.Fprintf(osStdout, "%s: egress allowlist is empty\n", slug)
			return 0
		}
		_, _ = fmt.Fprintf(osStdout, "%s: egress allowlist\n", slug)
		for _, prefix := range current {
			_, _ = fmt.Fprintf(osStdout, "  %s\n", prefix)
		}
		return 0
	case "clear":
		// An empty list, not nil: a nil slice encodes as null, which the API
		// reads as "leave unchanged", so clear reported success and kept the
		// list (production-us hunt #10, H10-37).
		current = []string{}
	case "add", "remove":
		if len(args) != 2 || args[1] == "" {
			PrintUsage(os.Stderr, "usage: gregale app <slug> egress-allowlist "+action+" <cidr>", "apps")
			return 1
		}
		prefix := args[1]
		if action == "add" {
			for _, existing := range current {
				if existing == prefix {
					return 0
				}
			}
			current = append(current, prefix)
		} else {
			filtered := current[:0]
			for _, existing := range current {
				if existing != prefix {
					filtered = append(filtered, existing)
				}
			}
			current = filtered
		}
	default:
		PrintUsage(os.Stderr, "usage: gregale app <slug> egress-allowlist {show|add <cidr>|remove <cidr>|clear}", "apps")
		return 1
	}
	updated, err := client.UpdateApp(context.Background(), slug, api.UpdateAppRequest{EgressAllowlist: &current})
	if err != nil {
		return printErr("Egress allowlist update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	if action == "clear" {
		PrintOK(osStdout, "App %s egress allowlist cleared.", slug)
	} else {
		PrintOK(osStdout, "App %s egress allowlist updated.", slug)
	}
	if egressAllowlistChangeNeedsRestart(before, current) {
		_, _ = fmt.Fprintf(osStdout, "Running instances keep their current egress rules until they next start; apply it now with `gregale app %s restart`.\n", slug)
	}
	return 0
}

// egressAllowlistChangeNeedsRestart mirrors vmmd's live-patch rule
// (pkg/fcvm egressAllowlistNeedsRestart): adding the first entry, clearing
// the list, or an address family gaining its first entry changes the
// instance's firewall chain shape, so running instances apply it when they
// next start (H10-36). Other edits apply to running instances in place.
func egressAllowlistChangeNeedsRestart(before, after []string) bool {
	if (len(before) == 0) != (len(after) == 0) {
		return true
	}
	families := func(list []string) (v4, v6 bool) {
		for _, raw := range list {
			if p, err := netip.ParsePrefix(raw); err == nil {
				if p.Addr().Is4() {
					v4 = true
				} else {
					v6 = true
				}
			}
		}
		return v4, v6
	}
	b4, b6 := families(before)
	a4, a6 := families(after)
	return a4 && !b4 || a6 && !b6
}
