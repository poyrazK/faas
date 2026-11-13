package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// renderIssueDetail is `debug issue get` without --json. It printed the raw
// response on production (2026-11-09) although every sibling command reads
// as text (ADR-20261110090000).
func renderIssueDetail(w io.Writer, d api.IssueDetail) {
	i := d.Issue
	_, _ = fmt.Fprintf(w, "%s\n", terminalSafe(i.Title))
	_, _ = fmt.Fprintf(w, "  ID:          %s (%s, %s)\n", i.ID, i.State, i.Environment)
	_, _ = fmt.Fprintf(w, "  Events:      %d, %d regressions\n", i.EventCount, i.RegressionCount)
	_, _ = fmt.Fprintf(w, "  Seen:        %s to %s\n", i.FirstSeenAt.UTC().Format(time.RFC3339), i.LastSeenAt.UTC().Format(time.RFC3339))
	if i.Impact24h != nil {
		_, _ = fmt.Fprintf(w, "  Customers:   %d identified in 24h\n", i.Impact24h.IdentifiedCustomers)
	}
	if len(d.Events) > 0 {
		e := d.Events[0]
		_, _ = fmt.Fprintf(w, "\nLatest event %s at %s (deployment %s)\n", e.EventID, e.OccurredAt.UTC().Format(time.RFC3339), e.DeploymentID)
		_, _ = fmt.Fprintf(w, "  %s: %s\n", terminalSafe(e.ExceptionType), terminalSafe(e.Message))
		if e.DebugRequestID != "" {
			_, _ = fmt.Fprintf(w, "  Request:     %s\n", e.DebugRequestID)
		}
		for _, line := range strings.Split(strings.TrimRight(e.StackTrace, "\n"), "\n") {
			if line != "" {
				_, _ = fmt.Fprintf(w, "    %s\n", terminalSafe(line))
			}
		}
	}
	if len(d.Releases) > 0 {
		_, _ = fmt.Fprintln(w, "\nReleases:")
		for _, r := range d.Releases {
			ref := r.CommitSHA
			if ref == "" {
				ref = r.DeploymentID
			}
			_, _ = fmt.Fprintf(w, "  %s  %d events, last %s\n", ref, r.EventCount, r.LastSeenAt.UTC().Format(time.RFC3339))
		}
	}
	if d.NextEventCursor != "" {
		_, _ = fmt.Fprintf(w, "\nOlder events: --cursor %s (or --json for every field)\n", d.NextEventCursor)
	}
}
