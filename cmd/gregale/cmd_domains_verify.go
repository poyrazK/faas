// cmd/gregale/cmd_domains_verify.go — `gregale domains verify` and
// `gregale domains show` (issue #961 / Mega-A PR-3).
//
// Both handlers are thin wrappers around the new SDK methods
// (VerifyDomain, GetDomain). The wire shape is the same
// CustomDomainResponse the existing list command renders; the
// `verify` handler additionally surfaces the cert NotAfter / SANs
// from the live cert dial the apid performs server-side.
//
// set-default is deliberately out of scope here — it requires a
// per-app default_domain column, which is a wider change tracked
// separately. The CustomDomainResponse.Default field has been
// added to the wire shape so a follow-up PR can light it up
// without a wire-scale change.

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDomainsVerify is the `gregale domains verify <domain>` handler.
// Calls apid's POST /v1/domains/{domain}/verify (idempotent) and
// prints the result. On 422 the CLI prints the problem code
// verbatim so the customer can grep their dashboard.
func cmdDomainsVerify(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: gregale domains verify <domain>\n")
		return 1
	}
	domain := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := client.VerifyDomain(ctx, domain)
	if err != nil {
		return printErr("Verify failed", err)
	}
	printDomainRow(d, true)
	return 0
}

// cmdDomainsShow is the `gregale domains show <domain>` handler.
// Calls apid's GET /v1/domains/{domain} and prints the row + cert
// details. The cert dial is on-demand; failures are surfaced as
// "cert: not yet issued" in the output so the customer knows to
// retry.
func cmdDomainsShow(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: gregale domains show <domain>\n")
		return 1
	}
	domain := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := client.GetDomain(ctx, domain)
	if err != nil {
		return printErr("Request failed", err)
	}
	printDomainRow(d, true)
	return 0
}

// cmdDomainsStatus renders the durable certificate lifecycle for every
// custom-domain binding. Unlike `show`, this command never performs a live
// TLS dial; it is safe for scripts and remains useful during an outage.
func cmdDomainsStatus(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "usage: gregale domains status\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	domains, err := client.ListDomains(ctx)
	if err != nil {
		return printErr("Request failed", err)
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
		fmt.Fprintln(os.Stdout)
	}
	return 0
}

// printDomainRow is the shared printer for both verify + show.
// When verbose is true, it also prints the cert NotAfter + SANs.
func printDomainRow(d api.CustomDomainResponse, verbose bool) {
	verified := statusPending
	if d.Verified {
		verified = statusVerified
	}
	fmt.Printf("%-40s %-12s %s\n", d.Domain, verified, d.AppID)
	if verbose && d.Verified {
		if d.CertNotAfter != "" {
			fmt.Printf("    cert_not_after: %s\n", d.CertNotAfter)
		}
		if len(d.CertSANs) > 0 {
			fmt.Printf("    cert_sans:      %v\n", d.CertSANs)
		}
	}
}
