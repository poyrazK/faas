// commands_registry.go — Tier D audit-gap close.
// `gregale registry <list|set|rm> --app <slug>` for per-app private
// container registry credentials (issue #461 / ADR-062).
//
// Mirrors commands_alerts.go exactly: same dispatcher shape, same flag
// conventions, same extract-validator pattern to keep each leaf under
// the 50-line handler cap (CLAUDE.md). The closed-set gate for
// --registry is a regex mirror of pkg/api/registry_auth.go:47 via the
// newly-exported api.RegistryHostRe() accessor.
//
// Auth: every route requires Bearer + MFA + ScopeRegistryCredsRead or
// ScopeRegistryCredsWrite (server-side, see handlers_registry_auth.go).
// Free-plan customers without RegistryCredentialMax > 0 get a 403
// `plan_registry_credentials_not_allowed` from the API.
//
// Output:
//   - list: --json emits the envelope (so quota_max survives); human
//     mode prints quota + one row per credential (registry, username,
//     created_at, last_used_at).
//   - set: --json emits the single AppRegistryCredentialResponse; human
//     mode prints the 4-row echo (registry/username/created/updated).
//     The plaintext password is NEVER echoed — the server seals it and
//     the response carries no password field.
//   - rm: --json emits `{"registry":"<h>","deleted":true}`; human mode
//     prints a one-line confirmation.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdRegistry dispatches `gregale registry <list|set|rm>` to the
// three leaves. Mirrors cmdAlerts (commands_alerts.go:40).
func cmdRegistry(args []string) int {
	parent, _ := lookupCliCommand("registry")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale registry <list|set|rm> --app <slug> [--registry <h>] [--user <u>] [--password <p>]", "registry")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdRegistryList(args[1:])
	case subAdd: // "set" surfaces as the `add` verb for grep-friendly parallelism with cmdAlertAdd.
		return cmdRegistrySet(args[1:])
	case subRm:
		return cmdRegistryRm(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown registry subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdRegistryList implements `gregale registry list --app <slug>`
// (GET /v1/apps/{slug}/registry-credentials). The envelope includes
// quota_max + count so the human renderer can show "2/5 hosts".
func cmdRegistryList(args []string) int {
	fs := flag.NewFlagSet("registry list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale registry list --app <slug>", "registry")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListAppRegistryCredentials(context.Background(), *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Quota: %d/%d\n", resp.Count, resp.QuotaMax)
	if len(resp.Credentials) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no credentials)")
		return 0
	}
	for _, c := range resp.Credentials {
		last := c.LastUsedAt
		if last == "" {
			last = "-"
		}
		_, _ = fmt.Fprintf(osStdout, "%-40s  %-32s  %s  last_used=%s\n", c.Registry, c.Username, c.CreatedAt, last)
	}
	return 0
}

// cmdRegistrySet implements `gregale registry set --app <slug>
// --registry <h> --user <u> --password <p>`
// (PUT /v1/apps/{slug}/registry-credentials). The password is plaintext
// at the CLI boundary only — the SDK ships it to the API, apid seals
// it via secretbox.SealBytes, and the response carries no password
// field. Never echoed back.
func cmdRegistrySet(args []string) int {
	fs := flag.NewFlagSet("registry set", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	registry := fs.String("registry", "", "registry host[:port] (required, lowercase DNS[:port])")
	username := fs.String("user", "", "username (required)")
	password := fs.String("password", "", "password (required, plaintext at CLI; sealed server-side)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !validateRegistrySetFlags(slug, registry, username, password) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.SetAppRegistryCredential(context.Background(), *slug, *registry, *username, *password)
	if err != nil {
		return printErr("Set failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Registry credential for %s set.", resp.Registry)
	_, _ = fmt.Fprintf(osStdout, "  username:    %s\n", resp.Username)
	_, _ = fmt.Fprintf(osStdout, "  created_at:  %s\n", resp.CreatedAt)
	_, _ = fmt.Fprintf(osStdout, "  updated_at:  %s\n", resp.UpdatedAt)
	return 0
}

// cmdRegistryRm implements `gregale registry rm --app <slug>
// --registry <h>` (DELETE /v1/apps/{slug}/registry-credentials).
// The registry query param must match the normalized form persisted
// server-side (lowercase, no scheme); the same regex gate as
// cmdRegistrySet runs locally so a typo costs zero latency.
func cmdRegistryRm(args []string) int {
	fs := flag.NewFlagSet("registry rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	registry := fs.String("registry", "", "registry host[:port] (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale registry rm --app <slug> --registry <h>", "registry")
		return 1
	}
	if !api.RegistryHostRe().MatchString(*registry) {
		return printErr("Invalid --registry", fmt.Errorf("must match lowercase DNS[:port]; got %q", *registry))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppRegistryCredential(context.Background(), *slug, *registry); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"registry": *registry, "deleted": true}))
	}
	PrintOK(osStdout, "Registry credential for %s removed.", *registry)
	return 0
}

// validateRegistrySetFlags enforces the per-field presence + range
// gates shared by cmdRegistrySet's body. Returns true on success;
// otherwise fires printErr with the matching error and returns false.
// Extracted to keep cmdRegistrySet under the 50-line handler cap.
func validateRegistrySetFlags(slug, registry, username, password *string) bool {
	if *slug == "" || *registry == "" || *username == "" || *password == "" {
		PrintUsage(os.Stderr, "usage: gregale registry set --app <slug> --registry <h> --user <u> --password <p>", "registry")
		return false
	}
	if !api.RegistryHostRe().MatchString(*registry) {
		printErr("Invalid --registry", fmt.Errorf("must match lowercase DNS[:port]; got %q", *registry))
		return false
	}
	if len(*username) > api.MaxRegistryUsernameLen {
		printErr("Invalid --user", fmt.Errorf("--user length %d exceeds %d", len(*username), api.MaxRegistryUsernameLen))
		return false
	}
	if len(*password) > api.MaxRegistryPasswordBytes {
		printErr("Invalid --password", fmt.Errorf("--password length %d exceeds %d", len(*password), api.MaxRegistryPasswordBytes))
		return false
	}
	return true
}
