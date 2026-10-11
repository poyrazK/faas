package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsWorkflowReplayPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events workflow-replay-preview", flag.ContinueOnError)
	workflowName := fs.String("workflow-name", "", "workflow name from captured event recipient snapshots")
	from := fs.String("from", "", "inclusive acceptance timestamp (RFC3339)")
	until := fs.String("until", "", "exclusive acceptance timestamp (RFC3339)")
	after := fs.String("after", "", "opaque continuation cursor; keep app, workflow and range unchanged")
	limit := fs.Int("limit", api.EventReplayPreviewPageDefault, "envelopes examined per page (1..100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	start, startErr := time.Parse(time.RFC3339Nano, *from)
	end, endErr := time.Parse(time.RFC3339Nano, *until)
	options := api.WorkflowEventReplayPreviewOptions{From: start, Until: end, After: *after, Limit: *limit}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*workflowName) == "" || startErr != nil || endErr != nil ||
		options.Validate() != nil || validateCLILimit("limit", *limit, api.EventReplayPreviewPageMax) != nil {
		PrintUsage(os.Stderr, "usage: gregale events workflow-replay-preview <app> --workflow-name NAME --from RFC3339 --until RFC3339 [--after CURSOR] [--limit N]", "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.PreviewWorkflowEventReplay(context.Background(), positional[0], *workflowName, options)
	if err != nil {
		return printErr("Workflow event replay preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	writeWorkflowEventReplayPreview(result)
	return 0
}

func writeWorkflowEventReplayPreview(r api.WorkflowEventReplayPreviewResponse) {
	_, _ = fmt.Fprintf(osStdout, "Workflow replay preview for %s / %s (read-only)\n", oneLine(r.AppSlug), oneLine(r.WorkflowName))
	_, _ = fmt.Fprintf(osStdout, "Acceptance window: [%s, %s) | fixed cutoff: %s\n", r.From.Format(time.RFC3339Nano), r.Until.Format(time.RFC3339Nano), r.CutoffAt.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(osStdout, "This page: examined %d | captured %d | trigger matches %d | filtered %d | not captured %d | unknown snapshot %d\n", r.ScannedCount, r.CapturedCount, r.MatchedCount, r.FilterMismatchCount, r.NotCapturedCount, r.UnknownRecipientCount)
	_, _ = fmt.Fprintf(osStdout, "Admission receipts: already admitted %d | potential new admissions %d. Potential admissions do not account for current workflow quotas or target availability.\n", r.AlreadyAdmittedCount, r.PotentialAdmissionCount)
	_, _ = fmt.Fprintf(osStdout, "Settled receipt retention: %s after routing settlement. Complete history is not guaranteed.\n", time.Duration(r.Retention.SettledRetentionSeconds)*time.Second)
	if r.Retention.EarliestRetainedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Earliest retained acceptance (account-wide): %s\n", r.Retention.EarliestRetainedAt.Format(time.RFC3339Nano))
	}
	_, _ = fmt.Fprintln(osStdout, "ACCEPTED\tSOURCE\tEVENT\tTYPE\tROUTING\tADMISSION\tRUN STATUS")
	for _, match := range r.Matches {
		admission := "not admitted"
		if match.AdmissionRecorded {
			admission = "deduplicated"
		}
		if match.AdmissionRecorded && match.WorkflowRunStatus == "" {
			admission += " (run no longer retained)"
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", match.AcceptedAt.Format(time.RFC3339Nano), oneLine(match.EventSource), oneLine(match.EventID), oneLine(match.EventType), oneLine(match.RoutingState), admission, oneLine(match.WorkflowRunStatus))
	}
	if r.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %s (keep the same app, workflow and range)\n", r.NextAfter)
	}
}
