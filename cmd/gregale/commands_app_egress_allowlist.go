package main

import (
	"context"
	"fmt"
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
		current = nil
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
	return 0
}
