package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsDeadlineAlertInvestigate(args []string) int {
	flags, positional := splitArgsForFlags(args, "explain")
	fs := newFlagSet("events deadline-alert-investigate", flag.ContinueOnError)
	file := fs.String("alert-file", "", "saved single Go/CLI lifecycle alert JSON object (maximum 64 KiB)")
	limit := fs.Int("limit", api.EventSubscriptionDeadlinePageDefault, "current recipients per page (1..100; default 50)")
	cursor := fs.String("cursor", "", "opaque next_cursor from the same app, alert selection and page size")
	explain := fs.Bool("explain", false, "read acceptance-guarded blocker explanations (requires limit 1..50)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || *file == "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events deadline-alert-investigate <app> --alert-file PATH [--limit 50] [--cursor CURSOR] [--explain]", "events")
		return 1
	}
	query := api.EventSubscriptionDeadlineQuery{Limit: *limit, Cursor: *cursor}
	if strings.TrimSpace(positional[0]) == "" || *limit < 1 || query.Validate() != nil {
		return printErr("Invalid investigation options", fmt.Errorf("require an app, limit 1..100 and a bounded cursor"))
	}
	if *explain && *limit > api.EventRecipientBlockerBatchMax {
		return printErr("Invalid explanation options", fmt.Errorf("--explain requires --limit 1..%d", api.EventRecipientBlockerBatchMax))
	}
	alert, err := readDeadlineInvestigationAlert(*file)
	if err != nil {
		return printErr("Invalid deadline alert file", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out, err := client.InvestigateEventDeadlineAlert(ctx, positional[0], alert, api.EventDeadlineInvestigationOptions{Limit: *limit, Cursor: *cursor, Explain: *explain})
	if err != nil {
		return printErr("Deadline alert investigation failed", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(out)); code != 0 {
			return code
		}
	} else {
		printDeadlineAlertInvestigation(out)
	}
	if out.Explanations != nil && !out.Explanations.ReadComplete {
		return 1
	}
	return 0
}

func readDeadlineInvestigationAlert(path string) (api.EventDeliveryHealthAlert, error) {
	var out api.EventDeliveryHealthAlert
	info, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.EventDeadlineInvestigationAlertFileMaxBytes {
		return out, fmt.Errorf("alert input must be a regular file of at most %d bytes", api.EventDeadlineInvestigationAlertFileMaxBytes)
	}
	raw, err := readSimulationJSONFile(path, api.EventDeadlineInvestigationAlertFileMaxBytes)
	if err != nil {
		return out, err
	}
	if err := rejectDuplicateLineageJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return out, fmt.Errorf("alert JSON contains duplicate or invalid fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return out, fmt.Errorf("alert file must contain one JSON object")
	}
	required := []string{"metric", "app_id", "subscription_id", "incident_id", "sequence", "transition", "at"}
	allowed := map[string]bool{}
	for _, name := range append(required, "rate_constraints", "metric_recording_since", "recovery_threshold", "recovery_sustain_seconds", "value", "threshold", "reason", "minimum_samples", "sustain_seconds") {
		allowed[name] = true
	}
	for name, value := range fields {
		if !allowed[name] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return out, fmt.Errorf("unknown, case-aliased or null alert field %q", name)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return out, fmt.Errorf("missing alert field %q", name)
		}
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func printDeadlineAlertInvestigation(out api.EventDeadlineInvestigation) {
	a, p, q := out.Alert, out.Page, out.Query
	_, _ = fmt.Fprintf(osStdout, "Alert %s %s | incident %s | sequence %d | app %s | subscription %s\n", oneLine(a.Metric), oneLine(a.Transition), oneLine(a.IncidentID), a.Sequence, oneLine(a.AppID), oneLine(a.SubscriptionID))
	_, _ = fmt.Fprintf(osStdout, "Alert notification: %s\nTriage window: %s | current observation: %s\n", a.AlertAt.Format(time.RFC3339Nano), p.WindowAt.Format(time.RFC3339Nano), p.ObservedAt.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintln(osStdout, "Membership: current at triage observation; original incident membership is not reconstructed")
	reason := q.WaitingReason
	if reason == "" {
		reason = "all"
	}
	_, _ = fmt.Fprintf(osStdout, "Selection: %s | relative horizon %dms | waiting reason %s | rows %d | limit %d\n", oneLine(q.Status), q.WithinMillis, oneLine(reason), len(p.Recipients), q.Limit)
	if len(p.Recipients) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No current rows in this page; this does not establish delivery success")
	} else {
		printDeadlineInvestigationRecipients(p.Recipients)
	}
	if out.Explanations != nil {
		printDeadlineInvestigationExplanations(out.Explanations)
	}
	_, _ = fmt.Fprintf(osStdout, "Has more: %t\n", p.HasMore)
	if p.HasMore {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %q\nContinue with the same app, alert file and --limit %d; supply this quoted value as --cursor.\n", p.NextCursor, q.Limit)
	}
}

func printDeadlineInvestigationExplanations(explanations *api.EventDeadlineExplanations) {
	_, _ = fmt.Fprintf(osStdout, "Explanations: selected triage page | attempted %d | rows %d | read complete %t\n", explanations.Attempted, len(explanations.Items), explanations.ReadComplete)
	_, _ = fmt.Fprintln(osStdout, "Diagnostic observations are separate reads; routing settlement does not establish handler completion")
	for _, item := range explanations.Items {
		_, _ = fmt.Fprintf(osStdout, "  Source %q | event %q | accepted %s\n", item.Query.Source, item.Query.ID, item.Query.ExpectedAcceptedAt.Format(time.RFC3339Nano))
		_, _ = fmt.Fprintf(osStdout, "    Diagnostic observation: %s | status %s | reason %s | routing state %s\n", deadlineExplanationStamp(item.ObservedAt), oneLine(item.Status), oneLine(item.Reason), oneLine(item.RoutingState))
		if r := item.Routing; r != nil {
			_, _ = fmt.Fprintf(osStdout, "    Waiting reason: %s | target available %t | capacity scope %s | next attempt %s | lease until %s\n", oneLine(r.WaitingReason), r.TargetAvailable, oneLine(r.CapacityScope), deadlineExplanationStamp(r.NextAttemptAt), deadlineExplanationStamp(r.LeaseUntil))
			if b := r.OrderingBlocker; b != nil {
				_, _ = fmt.Fprintf(osStdout, "    Ordering blocker: source %q | event %q | subscription %q | accepted %s | state %s | next attempt %s\n", b.EventSource, b.EventID, b.SubscriptionID, b.AcceptedAt.Format(time.RFC3339Nano), oneLine(b.State), deadlineExplanationStamp(b.NextAttemptAt))
			}
		}
	}
}

func deadlineExplanationStamp(at *time.Time) string {
	if at == nil {
		return "-"
	}
	return at.Format(time.RFC3339Nano)
}

func printDeadlineInvestigationRecipients(rows []api.EventSubscriptionDeadlineRecipient) {
	table := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "SOURCE\tEVENT ID\tACCEPTED AT\tDEADLINE\tEXPIRED NOW\tWAITING REASON\tNEXT ATTEMPT\tLEASE UNTIL")
	stamp := func(at *time.Time) string {
		if at == nil {
			return "-"
		}
		return at.Format(time.RFC3339Nano)
	}
	for _, r := range rows {
		_, _ = fmt.Fprintf(table, "%q\t%q\t%s\t%s\t%t\t%s\t%s\t%s\n", r.EventSource, r.EventID, r.AcceptedAt.Format(time.RFC3339Nano), r.DeadlineAt.Format(time.RFC3339Nano), r.Expired, oneLine(r.WaitingReason), stamp(r.NextAttemptAt), stamp(r.LeaseUntil))
	}
	_ = table.Flush()
}
