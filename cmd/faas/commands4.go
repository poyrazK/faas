package main

// G6 account self-service CLI commands (spec §17 G6, ADR-021).
//
//   faas account export [-o FILE] [--no-secrets]
//   faas account delete [-q]
//   faas account restore
//   faas account status
//
// All four route through the REST API the dashboard uses; the CLI
// never touches the store directly. Status is a thin alias for
// `faas whoami` but lives under the `account` namespace so the
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

// cmdAccount dispatches `faas account <subcommand>`.
func cmdAccount(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: faas account {export|delete|restore|status}")
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
	default:
		fmt.Fprintf(os.Stderr, "faas account: unknown subcommand %q\n", args[0])
		return 1
	}
}

// cmdAccountExport downloads the GDPR export bundle. Default output
// is faas-account-export.json in the cwd; -o picks another path.
// --no-secrets drops the ciphertext slice (the bundle still lists the
// apps + keys + usage without revealing the sealed envelope).
func cmdAccountExport(args []string) int {
	fs := flag.NewFlagSet("account export", flag.ContinueOnError)
	out := fs.String("o", "faas-account-export.json", "output file")
	noSecrets := fs.Bool("no-secrets", false, "exclude ciphertext slice")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.ExportAccount(context.Background(), *out, !*noSecrets); err != nil {
		return printErr("Export failed", err)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Printf("✓ Exported account data to %s\n", abs)
	return 0
}

// cmdAccountDelete schedules the 30-day grace deletion. Mirrors the
// `faas apps -q <slug>` y/N pattern (-q skips the prompt for CI).
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
				"You can cancel with `faas account restore` before the deadline. "+
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
	fmt.Printf("✓ Account scheduled for deletion\n")
	fmt.Printf("  status:       %s\n", resp.Status)
	fmt.Printf("  scheduled_at: %s\n", resp.ScheduledAt)
	fmt.Printf("  restore_until:%s\n", resp.RestoreUntil)
	fmt.Printf("\nCancel any time before the deadline with: faas account restore\n")
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
	fmt.Printf("✓ Account restored. Welcome back to the %s plan.\n", acct.Plan)
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
		fmt.Printf("\naccount scheduled for deletion — run `faas account restore` to cancel.\n")
	}
	return 0
}
