// cmd/gregale/cmd_domains_verify.go — `gregale domains verify` and
// `gregale domains show` (issue #961 / Mega-A PR-3).
//
// Both handlers are thin wrappers around the new SDK methods
// (VerifyDomain, GetDomain). The wire shape is the same
// CustomDomainResponse the existing list command renders; the
// `verify` handler additionally surfaces the cert NotAfter / SANs
// from the live cert dial the apid performs server-side.
//
// set-default is dispatched from commands2.go because it shares the legacy
// list/add/rm domain command surface; its durable state lives in the
// app_default_domains table.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDomainsVerify is the `gregale domains verify <domain>` handler.
// Calls apid's POST /v1/domains/{domain}/verify (idempotent) and
// prints the result. A pending response remains visible but returns
// exit 1 so scripts cannot treat ownership or a pending certificate as ready.
// JSON mode emits the CustomDomainResponse, including `verified`.
func cmdDomainsVerify(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, "usage: gregale domain verify <domain> [--wait] [--timeout 10m] [--poll-interval 5s]", "domains")
		return 0
	}
	fs := newFlagSet("domain verify", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait until DNS and HTTPS are ready")
	timeout := fs.Duration("timeout", 10*time.Minute, "readiness wait deadline")
	interval := fs.Duration("poll-interval", 5*time.Second, "readiness check interval (5s..1m)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *timeout <= 0 || *timeout > time.Hour || *interval < 5*time.Second || *interval > time.Minute {
		printCommandValidation(os.Stderr, "usage: gregale domain verify <domain> [--wait] [--timeout 10m] [--poll-interval 5s]\n")
		return 1
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fs.Arg(0))), ".")
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	var slugs map[string]string
	if !jsonOutput {
		slugs = appSlugsByID(client)
	}
	var expected, latest *api.CustomDomainResponse
	lastProgress := ""
	ended := func(err error) int {
		if jsonOutput && latest != nil {
			if code := jsonOut(writeJSON(*latest)); code != 0 {
				return code
			}
		}
		code := printErr("Verification wait ended", err)
		if signalCtx.Err() != nil {
			return 130
		}
		return code
	}
	for {
		requestCtx, requestCancel := context.WithTimeout(ctx, api.DomainVerificationManualTimeout+3*time.Second)
		d, err := client.VerifyDomain(requestCtx, domain)
		requestCancel()
		if err != nil {
			if ctx.Err() != nil {
				return ended(ctx.Err())
			}
			if !*wait || !domainSetupRetryable(err) {
				return printErr("Verify failed", err)
			}
		} else {
			if expected != nil && (d.Domain != expected.Domain || d.AppID != expected.AppID || d.Environment != expected.Environment || d.ChallengeToken != "" && expected.ChallengeToken != "" && d.ChallengeToken != expected.ChallengeToken) {
				PrintWarn(osStderr, "Domain binding changed while waiting; inspect domain get before retrying.")
				return 1
			}
			if expected == nil {
				first := d
				expected = &first
			}
			latest = &d
			ready := d.HTTPSReadyAt(time.Now())
			if !*wait || ready || d.VerificationExpiredAt(time.Now()) {
				if jsonOutput {
					if code := jsonOut(writeJSON(d)); code != 0 {
						return code
					}
				} else {
					printDomainRow(osStdout, d, slugs, true)
					if d.VerificationExpiredAt(time.Now()) {
						PrintWarn(osStdout, "Verification claim expired; recreate the domain binding to obtain a new TXT proof.")
					} else if !d.Verified {
						PrintWarn(osStdout, "Domain is still pending verification; check DNS records and retry.")
					} else if !ready && d.Readiness != nil {
						PrintWarn(osStdout, "Domain is not ready: %s; review the diagnostic checks and retry.", d.Readiness.Status)
					} else if !ready {
						PrintWarn(osStdout, "The TLS certificate is still pending or unavailable; check DNS and retry verification.")
					}
				}
				if ready {
					return 0
				}
				return 1
			}
			if !jsonOutput {
				progress := fmt.Sprintf("%t/%s/%s/%s/%v", d.Verified, d.CertStatus, d.CertLastError, d.DNSLastCheckedAt, d.Readiness)
				if progress != lastProgress {
					printDomainRow(osStdout, d, slugs, true)
					lastProgress = progress
				}
			}
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ended(ctx.Err())
		case <-timer.C:
		}
	}
}

// cmdDomainsShow is the `gregale domains show <domain>` handler.
// Calls apid's GET /v1/domains/{domain} and prints the row + cert
// details. The cert dial is on-demand; failures are surfaced as
// "cert: not yet issued" in the output so the customer knows to
// retry.
func cmdDomainsShow(args []string) int {
	if len(args) != 1 {
		printCommandValidation(os.Stderr, "usage: gregale domain get <domain>\n")
		return 1
	}
	domain := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var slugs map[string]string
	if !jsonOutput {
		slugs = appSlugsByID(client)
	}
	d, err := client.GetDomain(ctx, domain)
	if err != nil {
		return printErr("Could not read domain", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	printDomainRow(osStdout, d, slugs, true)
	return 0
}

// cmdDomainsStatus renders the durable certificate lifecycle for every
// custom-domain binding. Unlike `show`, this command never performs a live
// TLS dial; it is safe for scripts and remains useful during an outage.
func cmdDomainsStatus(args []string) int {
	// With a domain, status prints its conditions (ADR-20261030151810);
	// without one it lists every domain's certificate as before.
	if len(args) != 0 {
		return domainConditions.status(args)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	domains, err := client.ListDomains(ctx)
	if err != nil {
		return printErr("Could not list domains", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(domains))
	}
	for _, d := range domains {
		status := d.CertStatus
		if status == "" {
			status = "pending"
		}
		expires := d.CertExpiresAt
		if expires == "" {
			expires = d.CertNotAfter
		}
		if expires == "" {
			expires = "—"
		}
		fmt.Printf("%-40s %-10s %-25s", d.Domain, status, expires)
		if d.CertLastError != "" {
			fmt.Printf(" %s", d.CertLastError)
		}
		_, _ = fmt.Fprintln(os.Stdout)
	}
	return 0
}

// printDomainRow is the shared printer for both verify + show.
// When verbose is true, it also prints the cert NotAfter + SANs.
func printDomainRow(w io.Writer, d api.CustomDomainResponse, slugs map[string]string, verbose bool) {
	verified := statusPending
	if d.Verified {
		verified = statusVerified
	}
	target := appLabel(slugs, d.AppID)
	if d.Environment != "" {
		target += " [" + d.Environment + "]"
	}
	_, _ = fmt.Fprintf(w, "%-40s %-12s %s\n", d.Domain, verified, target)
	if verbose {
		printDomainDiagnostics(w, d)
	}
	if verbose && d.Verified {
		if d.CertNotAfter != "" {
			_, _ = fmt.Fprintf(w, "    cert_not_after: %s\n", d.CertNotAfter)
		}
		if len(d.CertSANs) > 0 {
			_, _ = fmt.Fprintf(w, "    cert_sans:      %v\n", d.CertSANs)
		}
	}
}

// printDomainDNSRecords prints the records the customer publishes for a
// custom domain (ADR-520). Older servers send only the TXT proof.
func printDomainDNSRecords(w io.Writer, d api.CustomDomainResponse) {
	if len(d.DNSRecords) == 0 {
		if d.ChallengeToken == "" {
			return
		}
		_, _ = fmt.Fprintf(w, "  _faas-verify.%s  TXT  %s\n", strings.TrimPrefix(d.Domain, "*."), d.ChallengeToken)
		return
	}
	for _, r := range d.DNSRecords {
		note := ""
		if r.Alternative {
			note = "  (at a zone apex, instead of the CNAME)"
		}
		_, _ = fmt.Fprintf(w, "  %s  %s  %s%s\n", r.Name, r.Type, r.Value, note)
	}
}

// printDomainDiagnostics exposes activation progress and actionable DNS checks
// without hiding record instructions after ownership verification succeeds.
func printDomainDiagnostics(w io.Writer, d api.CustomDomainResponse) {
	if d.Readiness != nil {
		_, _ = fmt.Fprintf(w, "    status: %s; ownership: %s; routing: %s; TLS: %s\n", d.Readiness.Status, d.Readiness.Ownership, d.Readiness.Routing, d.Readiness.TLS)
	}
	for _, item := range []struct{ name, value string }{
		{"last_checked", d.DNSLastCheckedAt}, {"next_check", d.VerificationNextCheckAt},
		{"claim_expires", d.VerificationExpiresAt}, {"certificate_retry", d.CertRetryAt},
	} {
		if item.value != "" {
			_, _ = fmt.Fprintf(w, "    %s: %s\n", item.name, item.value)
		}
	}
	if !d.Verified && d.VerificationNextCheckAt != "" {
		_, _ = fmt.Fprintf(w, "    verification_attempts: %d\n", d.VerificationAttempts)
	}
	for _, check := range d.Checks {
		if check.Status == "ok" || check.Status == "na" {
			continue
		}
		_, _ = fmt.Fprintf(w, "    %s: %s — %s\n", check.Name, check.Status, check.Detail)
		if check.Observed != "" {
			_, _ = fmt.Fprintf(w, "      observed: %s\n", check.Observed)
		}
		if check.Remediation != "" {
			_, _ = fmt.Fprintf(w, "      action: %s\n", check.Remediation)
		}
	}
	if !d.HTTPSReadyAt(time.Now()) && !d.VerificationExpiredAt(time.Now()) {
		printDomainDNSRecords(w, d)
	}
}
