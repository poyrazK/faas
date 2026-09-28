// cmd/gregale/cmd_domains_doctor.go — `gregale domains doctor <domain>`
// (ADR-120).
//
// The doctor handler calls apid's GET /v1/domains/{domain}/doctor
// and renders the 5-check report: dns_record / points_to_gregale /
// tls_certificate / caa_permits / ipv6_conflict. Each check has a
// stable name token + status (ok/fail/pending/na), a human-readable
// Detail, an Observed evidence field, and a Remediation line for
// failing checks — that last one is the load-bearing field for the
// activation drop-off the doctor is meant to fix.
//
// Output:
//   - default: hand-rolled rendering with ✓/✗ glyphs and a "Fix:"
//     section listing the failing remediations.
//   - `--json`: the raw DomainDoctorReport JSON.
//
// Exit code is 0 iff Healthy is true. A stale observation does not
// fail the command — it's surfaced as `stale: true` in the report
// (the handler's synchronous re-probe will have refreshed the row).

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDomainsDoctor is the `gregale domains doctor <domain>` handler.
// Calls apid's GET /v1/domains/{domain}/doctor and renders the
// 5-check report with remediation. Returns 0 on Healthy, 1
// otherwise; --json emits the raw DomainDoctorReport with the same
// health-based exit code.
func cmdDomainsDoctor(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: gregale domains doctor <domain>\n")
		return 1
	}
	domain := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report, err := client.DomainDoctor(ctx, domain)
	if err != nil {
		return printErr("Doctor failed", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		printDoctorReport(osStdout, report)
	}
	if report.Healthy {
		return 0
	}
	return 1
}

// printDoctorReport renders the DomainDoctorReport to w in the
// standard human form: a header (domain + status summary +
// observed_at), one line per check with a glyph (ok=✓ / fail=✗ /
// pending=… / na=·), and a "Fix:" section at the bottom listing the
// remediation lines for any failing check. A stale observation is
// surfaced as "stale" so the customer understands the data is older
// than FAAS_DOMAIN_DOCTOR_TTL_SECONDS.
//
// Glyphs are routed through output.Enabled() so they strip in pipes
// and under NO_COLOR (lint_tripwires_test.go::TestLintTripwire_NoGlyphLiteralOutsideOutput).
// All writes use _ = fmt.Fprint*(w, ...) — writer failures (closed
// pipe, broken TTY) are unrecoverable and we never want a status
// line to crash the CLI on its way out. Mirrors output.writeStatus.
func printDoctorReport(w io.Writer, r api.DomainDoctorReport) {
	failing := 0
	for _, c := range r.Checks {
		if c.Status == doctorCheckFail {
			failing++
		}
	}
	status := fmt.Sprintf("%d of %d checks failing", failing, len(r.Checks))
	if failing == 0 {
		if r.Healthy {
			status = fmt.Sprintf("all %d checks OK", len(r.Checks))
		} else {
			status = "domain is not ready"
		}
	}
	_, _ = fmt.Fprintf(w, "Domain:      %s\n", r.Domain)
	_, _ = fmt.Fprintf(w, "AppID:       %s\n", r.AppID)
	_, _ = fmt.Fprintf(w, "Status:      %s\n", status)
	_, _ = fmt.Fprintf(w, "Observed at: %s\n", r.ObservedAt)
	if r.Stale {
		_, _ = fmt.Fprintf(w, "Note:        stale — handler re-probed synchronously\n")
	}
	_, _ = fmt.Fprintln(w)
	for _, c := range r.Checks {
		marker := "?"
		switch c.Status {
		case doctorCheckOK:
			marker = glyphOK()
		case doctorCheckFail:
			marker = glyphFail()
		case doctorCheckPend:
			marker = "…"
		case doctorCheckNA:
			marker = "·"
		}
		_, _ = fmt.Fprintf(w, "%s %-22s %s\n", marker, c.Name, c.Detail)
		if c.Observed != "" {
			_, _ = fmt.Fprintf(w, "                                observed: %s\n", c.Observed)
		}
		if c.Remediation != "" {
			_, _ = fmt.Fprintf(w, "                                fix:       %s\n", c.Remediation)
		}
	}
	if failing > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Fix:")
		for _, c := range r.Checks {
			if c.Status == doctorCheckFail && c.Remediation != "" {
				_, _ = fmt.Fprintf(w, "  - %s\n", c.Remediation)
			}
		}
	}
}

// glyphOK returns the OK marker glyph when output coloring is on,
// an empty string otherwise. Mirrors output.PrintOK's contract.
func glyphOK() string {
	if Enabled() {
		return GlyphOK + " "
	}
	return ""
}

// glyphFail returns the FAIL marker glyph when output coloring is on,
// an empty string otherwise. Mirrors output.PrintFail's contract.
func glyphFail() string {
	if Enabled() {
		return GlyphFail + " "
	}
	return ""
}
