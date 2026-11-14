package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const accountHealthUsage = "usage: gregale account health [--json]"

// cmdAccountHealth prints GET /v1/account/health: what may stop the account
// from working, the same checks as /dashboard/account/health
// (ADR-20261112210000, ADR-20261114090000).
func cmdAccountHealth(args []string) int {
	fs := newFlagSet("account health", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, accountHealthUsage, "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	health, err := client.GetAccountHealth(context.Background())
	if err != nil {
		return printErr("Could not fetch account health", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(health))
	}
	renderAccountHealth(osStdout, health, dashboardBaseURL(client.BaseURL()))
	return 0
}

// renderAccountHealth writes the human view in the dashboard page's order:
// restrictions, recurring problems, failed deployments, domains, refused
// requests. dashboard turns the action paths into links.
func renderAccountHealth(w io.Writer, h api.AccountHealth, dashboard string) {
	// Deployment errors, hints and certificate errors carry text a build or a
	// certificate authority produced: every string goes through terminalSafe
	// so it cannot drive the terminal. --json keeps the raw values.
	p := func(format string, a ...any) {
		for i, v := range a {
			if s, ok := v.(string); ok {
				a[i] = terminalSafe(s)
			}
		}
		_, _ = fmt.Fprintf(w, format, a...)
	}
	stamp := func(t time.Time) string { return t.UTC().Format("02 Jan 2006, 15:04 UTC") }
	next := func(action, path string) {
		if path == "" {
			return
		}
		if action == "" {
			action = "Next step"
		}
		p("    %s: %s%s\n", action, dashboard, path)
	}

	p("Account health, checked %s\n", stamp(h.GeneratedAt))
	switch h.Attention {
	case 0:
		p("Nothing needs your attention.\n")
	case 1:
		p("1 thing needs your attention.\n")
	default:
		p("%d things need your attention.\n", h.Attention)
	}
	if len(h.Unavailable) > 0 {
		p("Some checks could not be loaded (%s); their sections may be incomplete.\n", strings.Join(h.Unavailable, ", "))
	}

	if len(h.Restrictions) > 0 {
		p("\nAccount\n")
		for _, r := range h.Restrictions {
			p("  ! %s\n    %s\n", r.Title, r.Detail)
			if r.Since != nil {
				p("    Since %s\n", stamp(*r.Since))
			}
			next(r.Action, r.ActionURL)
		}
	}
	if len(h.Signals) > 0 {
		p("\nKeeps going wrong\n")
		for _, s := range h.Signals {
			p("  ! %s\n    %s\n    Since %s\n", s.Title, s.Detail, stamp(s.Since))
			next(s.Action, s.ActionURL)
		}
	}
	if len(h.FailedDeployments) > 0 {
		p("\nFailed deployments, last 7 days\n")
		for _, d := range h.FailedDeployments {
			name := d.AppSlug
			if name == "" {
				name = d.AppID
			}
			p("  %s  %s", name, d.DeploymentID)
			if d.ErrorCode != "" {
				p("  %s", d.ErrorCode)
			}
			p("  %s\n", stamp(d.CreatedAt))
			if d.Error != "" {
				p("    %s\n", d.Error)
			}
			if d.Hint != "" {
				p("    How to fix: %s\n", d.Hint)
			}
		}
	}
	if len(h.DomainIssues) > 0 {
		p("\nCustom domains\n")
		for _, d := range h.DomainIssues {
			p("  %s: %s", d.Domain, d.Problem)
			if d.CertError != "" {
				p(": %s", d.CertError)
			}
			p("\n")
			if d.AppSlug != "" {
				next("Run the domain doctor", "/dashboard/apps/"+url.PathEscape(d.AppSlug)+"/domains/"+url.PathEscape(d.Domain)+"/doctor")
			}
		}
	}

	req := h.RefusedRequests
	p("\nRefused API requests, last %d hours: %d\n", req.WindowHours, req.Total)
	for _, c := range req.ByCode {
		p("  %s x%d, last %s\n", c.Code, c.Count, c.LastAt.UTC().Format("02 Jan 15:04 UTC"))
	}
	if len(req.Recent) > 0 {
		p("  Most recent:\n")
		for _, r := range req.Recent {
			line := fmt.Sprintf("    %s  %s  %s", r.OccurredAt.UTC().Format("02 Jan 15:04:05"), r.Route, r.Code)
			if r.RequestID != "" {
				line += "  request " + r.RequestID
			}
			p("%s\n", line)
		}
		p("  Ask support about a request: %s/dashboard/support?request_id=REQUEST_ID#new-ticket\n", dashboard)
	}
}
