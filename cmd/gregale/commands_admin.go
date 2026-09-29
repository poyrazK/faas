// Issue #279 — operator CLI surface for billing operations.
//
// `gregale admin credit <account_uuid> <cents> --reason <text>` and
// `gregale admin refund <account_uuid> <invoice_uuid> <cents> --reason <text>`
// are the operator's billing operations that do not require leaving the
// platform. The dispatch is single-level: `gregale admin <sub>`. Future
// provider-specific operations land as additional dispatch arms in cmdAdmin.
//
// Auth model: the SDK call requires an admin-scoped API key
// (ScopesAdminOnly) AND the caller's email must be in
// FAAS_ADMIN_EMAILS. Both layers are enforced server-side; the CLI
// just surfaces a friendlier error if the call returns 403.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAdmin is the dispatcher for `gregale admin <subcommand>`. New
// subcommands go here. Empty args / unknown subcommands print
// usage and return 2 (the CLI convention for "operator error").
//
// Flag convention: flags precede positional args, mirroring Go's
// flag package (and the existing `gregale account export --no-secrets`
// pattern at commands4.go:146). `--reason <text>` must come before
// the account uuid + cents positionals.
func cmdAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale admin <credit|refund|consume-credits|abuse-hold|egress-flows>")
		fmt.Fprintln(os.Stderr, "  gregale admin credit --reason <text> <account_uuid> <cents>")
		fmt.Fprintln(os.Stderr, "  gregale admin refund --reason <text> [--idempotency-key K] <account_uuid> <invoice_uuid> <cents>")
		fmt.Fprintln(os.Stderr, "  gregale admin consume-credits <invoice-id>")
		fmt.Fprintln(os.Stderr, "  gregale admin abuse-hold <place|release> --note <text> <account_uuid>")
		fmt.Fprintln(os.Stderr, "  gregale admin egress-flows [--remote ip|cidr] [--account id] [--from t] [--to t] [--limit n]")
		PrintUsage(os.Stderr, "usage: gregale admin <subcommand>", "admin")
		return 2
	}
	switch args[0] {
	case "credit":
		return cmdAdminCredit(args[1:])
	case "refund":
		return cmdAdminRefund(args[1:])
	case "consume-credits":
		return cmdAdminConsumeCredits(args[1:])
	case "abuse-hold":
		return cmdAdminAbuseHold(args[1:])
	case "egress-flows":
		return cmdAdminEgressFlows(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale: unknown admin subcommand %q\n", args[0])
		return 2
	}
}

// cmdAdminRefund refunds a paid Polar order selected by the local invoice ID.
// The CLI derives a stable operation key from the full refund intent unless
// the operator supplies --idempotency-key, which permits two deliberate
// same-amount partial refunds to remain distinct operations.
func cmdAdminRefund(args []string) int {
	fs := newFlagSet("admin refund", flag.ContinueOnError)
	reason := fs.String("reason", "", "reason text (required, 3..500 chars)")
	idem := fs.String("idempotency-key", "", "stable provider idempotency key (optional)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: gregale admin refund --reason <text> [--idempotency-key K] <account_uuid> <invoice_uuid> <cents>")
		return 2
	}
	accountUUID, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregale: account must be a UUID")
		return 2
	}
	invoiceUUID, err := uuid.Parse(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregale: invoice must be a UUID")
		return 2
	}
	cents, err := strconv.ParseInt(fs.Arg(2), 10, 64)
	if err != nil || cents <= 0 {
		fmt.Fprintln(os.Stderr, "gregale: cents must be a positive integer (in EUR cents)")
		return 2
	}
	if n := len(strings.TrimSpace(*reason)); n < 3 || n > 500 {
		fmt.Fprintln(os.Stderr, "gregale: --reason must be 3..500 chars")
		return 2
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	key := strings.TrimSpace(*idem)
	if key == "" {
		h := sha256.Sum256([]byte(accountUUID.String() + "\x00" + invoiceUUID.String() + "\x00" + strconv.FormatInt(cents, 10) + "\x00" + strings.TrimSpace(*reason)))
		key = "cli-admin-refund-" + hex.EncodeToString(h[:16])
	}
	resp, err := client.RefundAccount(context.Background(), accountUUID.String(), invoiceUUID.String(), key, cents, strings.TrimSpace(*reason))
	if err != nil {
		return printErr("Refund failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Refunded %d cents for invoice %s.", resp.AmountCents, resp.InvoiceID)
	_, _ = fmt.Fprintf(osStdout, "  provider:  %s\n", resp.Provider)
	_, _ = fmt.Fprintf(osStdout, "  refund:    %s\n", resp.ProviderRefundID)
	_, _ = fmt.Fprintf(osStdout, "  status:    %s\n", resp.Status)
	return 0
}

// cmdAdminCredit issues an account credit via POST /v1/admin/
// accounts/{id}/credits. The Idempotency-Key is derived from a stable
// hash of (account_uuid, cents, reason) so a flaky-network retry —
// or a `make` re-run that re-issues the same operator intent — returns
// the same credit_id rather than minting a duplicate account_credits
// row.
//
// Note: cmdAccountDelete's `cli-delete-<random>` key is one-shot by
// design (each `gregale account delete` invocation is its own operator
// intent and deduping across invocations would mask double-deletes).
// Credit issuance is different: the same (account, cents, reason)
// tuple is the *same* operator intent, and a network blip should not
// land a duplicate goodwill credit. Hashing the tuple captures that.
//
// Account argument is the target's UUID, not the email. The server
// is the source of truth for account lookup; if the UUID is unknown
// the handler returns 404 with CodeNotFound. We validate the UUID
// shape client-side for a faster, friendlier 2.
func cmdAdminCredit(args []string) int {
	fs := newFlagSet("admin credit", flag.ContinueOnError)
	reason := fs.String("reason", "", "reason text (required, 3..500 chars)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: gregale admin credit --reason <text> <account_uuid> <cents>")
		return 2
	}
	accountUUID, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregale: account must be a UUID")
		return 2
	}
	cents, err := strconv.ParseInt(fs.Arg(1), 10, 64)
	if err != nil || cents <= 0 {
		fmt.Fprintln(os.Stderr, "gregale: cents must be a positive integer (in EUR cents)")
		return 2
	}
	if *reason == "" {
		fmt.Fprintln(os.Stderr, "gregale: --reason is required (3..500 chars)")
		return 2
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Stable Idempotency-Key so a retry returns the same credit_id.
	// The server stores the response for 24 h keyed on (caller, key).
	// Hashing the (account, cents, reason) tuple captures operator
	// intent: re-running the exact same command is a retry, not a new
	// credit. SHA-256 is overkill for a dedupe key but keeps the
	// prefix-length consistent and avoids accidental collisions
	// across (uuid, cents, reason) tuples that differ only in
	// boundary chars.
	h := sha256.Sum256([]byte(accountUUID.String() + "\x00" + strconv.FormatInt(cents, 10) + "\x00" + *reason))
	key := "cli-admin-credit-" + hex.EncodeToString(h[:16])
	resp, err := client.IssueAccountCredit(context.Background(), accountUUID.String(), key, cents, *reason)
	if err != nil {
		return printErr("Issue failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Issued credit %s for %d cents (remaining=%d) to %s",
		resp.ID, cents, resp.CentsRemaining, resp.AccountID)
	_, _ = fmt.Fprintf(osStdout, "  reason:    %s\n", resp.Reason)
	_, _ = fmt.Fprintf(osStdout, "  created:   %s\n", resp.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	return 0
}

// cmdAdminConsumeCredits drains an invoice's prepaid credits via
// POST /v1/invoices/{id}/consume-credits (Tier B audit gap; the
// dashboard has the same operation, the CLI did not). Operator
// flow: an invoice was issued against a credit balance; once the
// customer pays the invoice, the operator flips the credits from
// "reserved for this invoice" to "consumed" so the audit log
// reflects the actual cash settlement.
//
// Auth model: same as cmdAdminCredit — admin-scoped key + email
// allowlist + MFA. Idempotency-Key is auto-minted by the SDK when
// none is supplied, so a flaky-network retry is safe (the server
// returns the same response for 24 h).
//
// Pre-flight scope check: the SDK does not expose the active key's
// scopes client-side (bearer tokens are opaque, not JWT, and the
// /v1/account response deliberately omits the scope field so a stolen
// token read can't enumerate them). The leaf therefore cannot fail
// closed before the round-trip — a non-admin key will hit 403 via
// the server's requireScope(ScopesAdminOnly) gate, and printErr
// renders the resulting APIError so the operator sees the precise
// failure mode. A future server-side endpoint that returns the
// active key's scopes (issue #TBD) would let this leaf short-circuit
// without a network round-trip; for now the 403 round-trip is the
// authoritative answer.
func cmdAdminConsumeCredits(args []string) int {
	fs := newFlagSet("admin consume-credits", flag.ContinueOnError)
	idem := fs.String("idempotency-key", "", "Idempotency-Key (optional; SDK mints one if empty)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale admin consume-credits [--idempotency-key K] <invoice-id>")
		return 2
	}
	invoiceID := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ConsumeInvoiceCredits(context.Background(), invoiceID, *idem)
	if err != nil {
		// Render the 403 with a hint that scopes are the likely cause —
		// the server's Problem title is "Forbidden" and the detail
		// names the missing scope, but a customer typing the command
		// for the first time benefits from the breadcrumb.
		var ae *APIError
		if errors.As(err, &ae) && ae.Problem.Status == http.StatusForbidden {
			return printErr("Consume-credits failed (likely missing admin scope on the active API key — use `gregale keys ls` to check, or rotate to an admin-scoped key)",
				err)
		}
		return printErr("Consume-credits failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Consumed credits on invoice %s: %d cents consumed (%d remaining).", resp.InvoiceID, resp.ConsumedCents, resp.RemainingCreditsCents)
	return 0
}

// cmdAdminAbuseHold places or releases an ADR-361 account abuse hold. A held
// account runs and deploys nothing; schedd places egress_fanout holds on its
// own, and an operator releases them after review.
func cmdAdminAbuseHold(args []string) int {
	const usage = "usage: gregale admin abuse-hold <place|release> --note <text> <account_uuid>"
	if len(args) == 0 || (args[0] != "place" && args[0] != "release") {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	place := args[0] == "place"
	fs := newFlagSet("admin abuse-hold "+args[0], flag.ContinueOnError)
	note := fs.String("note", "", "audit note (required, 3..500 chars)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	accountUUID, err := uuid.Parse(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregale: account must be a UUID")
		return 2
	}
	if n := len(*note); n < 3 || n > 500 {
		fmt.Fprintln(os.Stderr, "gregale: --note is required (3..500 chars)")
		return 2
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	change := client.ReleaseAccountAbuseHold
	if place {
		change = client.PlaceAccountAbuseHold
	}
	resp, err := change(context.Background(), accountUUID.String(), *note)
	if err != nil {
		return printErr("Abuse hold change failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	switch {
	case resp.AbuseHold != nil && resp.Changed:
		PrintOK(osStdout, "Held account %s (%s)", resp.AccountID, resp.AbuseHold.Reason)
	case resp.AbuseHold != nil:
		PrintOK(osStdout, "Account %s was already held (%s since %s)", resp.AccountID, resp.AbuseHold.Reason,
			resp.AbuseHold.HeldAt.Format("2006-01-02T15:04:05Z07:00"))
	case resp.Changed:
		PrintOK(osStdout, "Released account %s; its apps wake on their next request", resp.AccountID)
	default:
		PrintOK(osStdout, "Account %s was not held", resp.AccountID)
	}
	return 0
}

// cmdAdminEgressFlows searches the ADR-369 egress flow log: which tenant
// opened flows to an address in a window. Times are RFC 3339.
func cmdAdminEgressFlows(args []string) int {
	fs := newFlagSet("admin egress-flows", flag.ContinueOnError)
	remote := fs.String("remote", "", "remote IP address or CIDR")
	account := fs.String("account", "", "account id")
	from := fs.String("from", "", "window start, RFC 3339 (default: 24h before --to)")
	to := fs.String("to", "", "window end, RFC 3339 (default: now)")
	limit := fs.Int("limit", 0, "maximum rows (default 200, max 1000)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	q := api.EgressFlowQuery{Remote: *remote, AccountID: *account, Limit: *limit}
	for _, p := range []struct {
		name, val string
		dst       *time.Time
	}{{"from", *from, &q.From}, {"to", *to, &q.To}} {
		if p.val == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, p.val)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gregale: --%s must be an RFC 3339 time\n", p.name)
			return 2
		}
		*p.dst = t
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListEgressFlows(context.Background(), q)
	if err != nil {
		return printErr("Egress flow search failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Flows) == 0 {
		PrintOK(osStdout, "No egress flows match")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-20s  %-8s  %-32s  %-32s  %s\n", "OBSERVED", "NODE", "ACCOUNT", "APP", "REMOTE")
	for _, f := range resp.Flows {
		_, _ = fmt.Fprintf(osStdout, "%-20s  %-8s  %-32s  %-32s  %s:%d\n",
			f.ObservedAt.UTC().Format("2006-01-02T15:04:05Z"), f.Node, f.AccountID, f.AppID, f.RemoteIP, f.RemotePort)
	}
	if resp.Truncated {
		_, _ = fmt.Fprintln(osStdout, "(page limit reached; narrow the window or filter to see more)")
	}
	return 0
}
