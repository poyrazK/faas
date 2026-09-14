// commands_github_webhook_secret.go — `gregale
// github-webhook-secret set` subcommand (PR-D / ADR-012 §7
// amendment).
//
// Distinct top-level command (mirrors `secrets rotate`'s posture)
// so a per-tenant webhook secret rotation is a single deliberate
// operation with its own audit trail. The CLI never sees the
// plaintext secret except at the `--secret` flag boundary; the
// load-bearing difference from `secrets` is that the secret is
// written to the per-tenant row at GitHub App install granularity
// (installation_id), not the per-app row.
//
//   gregale github-webhook-secret set --installation-id <id> --secret <hex> [--from-stdin]
//
// `--from-stdin` reads one line of `<hex>` from stdin so the
// secret never lands in shell history. Mirrors `secrets
// rotate --from-stdin` for consistency.
//
// Rotation semantics: the new secret replaces the old one
// (ON CONFLICT DO UPDATE in the SQL). The Prometheus counter
// `githubd_webhook_secret_total{status="set"}` is emitted
// server-side at the apid handler. The CLI's contract is:
//   - exit 0 on success
//   - exit 1 on Problem (server-side validation)
//   - exit 1 on transport (with --json shape so the operator can
//     distinguish in a CI loop)

package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// githubWebhookSecretSet is the single subcommand under
// `github-webhook-secret`. The verb is "set" — read/write both
// rotate (update if row exists). The CLI doesn't expose a
// "delete" verb because the per-tenant secret is intentionally
// permanent; an install that wants to drop the per-tenant
// override must delete the row via the platform secret's
// fallback at the runbook
// (docs/runbooks/GithubWebhookSecretRotation.md).
func githubWebhookSecretSet(args []string) int {
	fs := newFlagSet("github-webhook-secret set", flag.ContinueOnError)
	installationID := fs.Int64("installation-id", 0, "GitHub App installation_id for the secret")
	secret := fs.String("secret", "", "secret hex (must be 32-64 hex chars; --from-stdin reads the same)")
	fromStdin := fs.Bool("from-stdin", false, "read the secret hex from stdin (one line)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *installationID == 0 {
		PrintUsage(os.Stderr,
			"usage: gregale github-webhook-secret set --installation-id <id> --secret <hex> [--from-stdin]",
			"github-webhook-secret")
		return 1
	}

	// Source the secret value: either from --secret or from
	// stdin. Mixed sources (both set) is an error so the
	// operator can't accidentally use the wrong value.
	if *fromStdin {
		if *secret != "" {
			fmt.Fprintln(os.Stderr, "github-webhook-secret: --from-stdin and --secret are mutually exclusive")
			return 1
		}
		scanner := bufio.NewScanner(osStdin)
		scanner.Buffer(make([]byte, 0, 256), 256)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			*secret = line
			break
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr, "github-webhook-secret: read stdin:", err)
			return 1
		}
	}
	if *secret == "" {
		fmt.Fprintln(os.Stderr, "github-webhook-secret: --secret (or --from-stdin) is required")
		return 1
	}

	// Hex-decode. The server stores raw bytes; the CLI takes
	// hex so the secret never has to be a binary value on the
	// command line or in shell history.
	raw, err := hex.DecodeString(strings.TrimSpace(*secret))
	if err != nil {
		fmt.Fprintln(os.Stderr, "github-webhook-secret: --secret must be valid hex:", err)
		return 1
	}
	if len(raw) < 16 || len(raw) > 64 {
		// 16 bytes = 128-bit entropy (the GitHub minimum);
		// 64 bytes = 512-bit (the suggested maximum). The
		// server-side SQL has no upper bound at the format
		// level, but enforcing here keeps the secret sane.
		fmt.Fprintln(os.Stderr, "github-webhook-secret: secret must be 16-64 bytes (32-128 hex chars)")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// The server route is admin-scoped (apids's
	// /v1/admin/github-webhook-secrets); the admin scope is
	// already implied by an FAAS_TOKEN with the
	// ScopesAdminWebhookSecret set (PR-D initial scope — same
	// shape as ScopesAdminCredit). The route returns an
	// updated_at + upgraded_by row so the on-call can confirm
	// the rotation landed.
	resp, err := client.SetGithubWebhookSecret(ctx, api.AdminSetGithubWebhookSecretRequest{
		InstallationID: *installationID,
		SecretHex:      hex.EncodeToString(raw),
	})
	if err != nil {
		return printErr("github-webhook-secret set", err)
	}

	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "github-webhook-secret: installation_id=%d upgraded_at=%s upgraded_by=%s",
		*installationID, resp.UpgradedAt.Format(time.RFC3339), resp.UpgradedBy)
	return 0
}
