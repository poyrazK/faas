package main

// G6 account self-service CLI commands (spec §17 G6, ADR-021).
//
//   gregale account export [-o FILE] [--no-secrets]
//   gregale account delete [-q]
//   gregale account restore
//   gregale account status
//
// All four route through the REST API the dashboard uses; the CLI
// never touches the store directly. Status is a thin alias for
// `gregale whoami` but lives under the `account` namespace so the
// discoverable help text points operators to the right command.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdAccount dispatches `gregale account <subcommand>`.
func cmdAccount(args []string) int {
	parent, _ := lookupCliCommand("account")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale account {export|delete|restore|status|dpa|slo}", "account")
		return 1
	}
	switch args[0] {
	case "export":
		return cmdAccountExport(args[1:])
	case "delete":
		return cmdAccountDelete(args[1:])
	case "restore":
		return cmdAccountRestore(args[1:])
	case statusLiteral:
		return cmdAccountStatus(args[1:])
	case "dpa":
		// Tier B audit gap: the Data-Processing-Addendum text (spec
		// §17 G6) is published at GET /v1/account/dpa (markdown;
		// no auth — the URL is also reachable from /security). The
		// CLI surfaces the same body so operators can `curl`-equiv
		// from a CI box or pin the response in a contract test.
		return cmdAccountDPA(args[1:])
	case "slo":
		// Move 2 PR-A: CLI twin for GET /v1/account/slo
		// (issue #696 / ADR-082). Account-wide SLO rollup.
		return cmdAccountSLO(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale account: unknown subcommand %q\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdAccountExport downloads the GDPR export bundle. Default output
// is gregale-account-export.json in the cwd; -o picks another path.
// --no-secrets drops the ciphertext slice (the bundle still lists the
// apps + keys + usage without revealing the sealed envelope).
func cmdAccountExport(args []string) int {
	fs := flag.NewFlagSet("account export", flag.ContinueOnError)
	out := fs.String("o", "gregale-account-export.json", "output file")
	noSecrets := fs.Bool("no-secrets", false, "exclude ciphertext slice")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := ExportAccountFile(client, context.Background(), *out, !*noSecrets); err != nil {
		return printErr("Export failed", err)
	}
	abs, _ := filepath.Abs(*out)
	PrintOK(osStdout, "Exported account data to %s", abs)
	return 0
}

// cmdAccountDelete schedules the 30-day grace deletion. Mirrors the
// `gregale apps -q <slug>` y/N pattern (-q skips the prompt for CI).
func cmdAccountDelete(args []string) int {
	fs := flag.NewFlagSet("account delete", flag.ContinueOnError)
	quiet := fs.Bool("q", false, "suppress confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr,
			"This will schedule your account for permanent deletion in 30 days. "+
				"You can cancel with `gregale account restore` before the deadline. "+
				"Continue? [y/N] ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("aborted")
			return 1
		}
	}
	// Idempotency-Key so retries on a flaky connection get the same
	// envelope back. The random nonce is one-shot; the server stores
	// the response for 24 h keyed on (account, nonce).
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	key := "cli-delete-" + hex.EncodeToString(nonce)
	resp, err := client.DeleteAccount(context.Background(), key)
	if err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Account scheduled for deletion")
	fmt.Printf("  status:       %s\n", resp.Status)
	fmt.Printf("  scheduled_at: %s\n", resp.ScheduledAt)
	fmt.Printf("  restore_until:%s\n", resp.RestoreUntil)
	fmt.Printf("\nCancel any time before the deadline with: gregale account restore\n")
	return 0
}

// cmdAccountRestore cancels a pending deletion. Always succeeds when
// inside the grace window; the server returns 409 past the deadline.
func cmdAccountRestore(args []string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.RestoreAccount(context.Background())
	if err != nil {
		return printErr("Restore failed", err)
	}
	PrintOK(osStdout, "Account restored. Welcome back to the %s plan.", acct.Plan)
	return 0
}

// cmdAccountStatus prints the account + plan + status + deletion
// deadline (if pending). Thin wrapper around Whoami with G6-aware
// formatting.
func cmdAccountStatus(args []string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Status failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(acct))
	}
	fmt.Printf("account: %s\n", acct.Email)
	fmt.Printf("plan:    %s\n", acct.Plan)
	fmt.Printf("status:  %s\n", acct.Status)
	fmt.Printf("apps:    %d\n", acct.AppCount)
	if acct.Status == "deleted_pending" {
		fmt.Printf("\naccount scheduled for deletion — run `gregale account restore` to cancel.\n")
	}
	return 0
}

// cmdAccountDPA fetches the platform's Data-Processing-Addendum
// text (spec §17 G6 / ADR-018) from GET /v1/account/dpa. The route
// is unauth-friendly on the server (it's also reachable from
// /security); the CLI uses `NewClient(apiBase(), loadToken())`
// directly — NOT authedClient() — because a freshly-installed box
// must be able to read the DPA before logging in. The token is
// harmless if it's set (the server ignores it on this route) and the
// SDK tolerates an empty token without failing client-side.
//
// Default output is stdout (markdown). `-o FILE` writes to disk —
// operators pin the response in CI contract tests so a server-side
// edit to the DPA shows up as a PR diff. The body is small (~3 KB)
// so there's no streaming story here; one buffer write.
func cmdAccountDPA(args []string) int {
	fs := flag.NewFlagSet("account dpa", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale account dpa [-o FILE]", "account")
		return 1
	}
	// DPA route is unauth-friendly on the server (it's also reachable
	// from /security). Pass the stored token if present (no-op server
	// side) but never require it — `gregale account dpa` works on a
	// freshly-installed box before login.
	client := NewClient(apiBase(), loadToken())
	body, err := client.GetAccountDPA(context.Background())
	if err != nil {
		return printErr("DPA fetch failed", err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, body, 0o644); err != nil {
			return printErr("Write failed", err)
		}
		abs, _ := filepath.Abs(*out)
		PrintOK(osStdout, "Wrote %d bytes of DPA text to %s", len(body), abs)
		return 0
	}
	if _, err := osStdout.Write(body); err != nil {
		return printErr("Write failed", err)
	}
	return 0
}
