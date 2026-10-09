package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRoutesLifecycleHistory(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes lifecycle history", flag.ContinueOnError)
	limit := fs.Int("limit", api.RouteLifecycleHistoryPageSize, "reviews per page")
	before := fs.String("before", "", "retained review ID from next_cursor")
	if fs.Parse(flags) != nil {
		return 1
	}
	validCursor := true
	if *before != "" {
		id, err := strconv.ParseInt(*before, 10, 64)
		validCursor = err == nil && id > 0 && strconv.FormatInt(id, 10) == *before
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *limit < 1 || *limit > api.RouteLifecycleHistoryMaxPage || !validCursor || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle history <slug> [--limit N] [--before REVIEW_ID] [--json]", "cli")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page, err := client.ListRouteLifecycleHistory(ctx, positional[0], *limit, *before)
	if err != nil {
		return printErr("Could not read lifecycle history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No production lifecycle reviews.")
		return 0
	}
	for _, e := range page.Entries {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  deployment=%s  scope=%s  recovery=%t\n", previewReportText(e.ID), e.ReviewedAt.Format(time.RFC3339), previewReportText(e.Outcome), previewReportText(e.DeploymentID), previewReportText(e.Scope), e.Recovery)
		if len(e.Decision.Reasons) > 0 {
			_, _ = fmt.Fprintf(osStdout, "  reasons: %s\n", previewReportText(strings.Join(e.Decision.Reasons, ", ")))
		}
		for _, a := range e.Approvals {
			_, _ = fmt.Fprintf(osStdout, "  approval=%s used=%t status=%s reason=%s\n", previewReportText(a.ID), a.Used, previewReportText(a.Status), previewReportText(a.StatusReason))
		}
		if !e.EvidenceAvailable {
			_, _ = fmt.Fprintln(osStdout, "  Historical binding evidence was not recorded.")
		} else if e.Truncated {
			_, _ = fmt.Fprintln(osStdout, "  Binding metadata truncated; read approval receipts for full pins.")
		}
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --before %s\n", previewReportText(page.NextCursor))
	}
	return 0
}
