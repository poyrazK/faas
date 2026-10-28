package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type lineageJobExportSkip struct {
	OutboxID      int64  `json:"outbox_id"`
	SourceOutcome string `json:"source_outcome"`
	Reason        string `json:"reason"`
}

type lineageJobExportSummary struct {
	Format          string                 `json:"format,omitempty"`
	AppID           string                 `json:"app_id"`
	File            string                 `json:"file,omitempty"`
	RequestID       string                 `json:"request_id,omitempty"`
	SourceRequestID string                 `json:"source_request_id"`
	SourceCreatedAt time.Time              `json:"source_created_at"`
	App             string                 `json:"app"`
	SubscriptionID  string                 `json:"subscription_id"`
	Selected        int                    `json:"selected"`
	SourceOutcomes  map[string]int         `json:"source_outcomes"`
	Reasons         map[string]int         `json:"reasons"`
	Skipped         []lineageJobExportSkip `json:"skipped"`
}

func cmdEventsLineageRetryJobExport(args []string) int {
	flags, positional := splitArgsForFlags(args, "raw")
	fs := newFlagSet("subscription-lineage-retry-job-export", flag.ContinueOnError)
	rawOutput := fs.Bool("raw", false, "export a legacy unscoped API request body")
	file := fs.String("file", "", "new request JSON path; never overwrite")
	outcomes := fs.String("outcomes", "unresolved,conflict,changed,unavailable", "comma-separated original outcomes; cancelled also allowed")
	expected := fs.String("expected-created-at", "", "required original creation timestamp from the reviewed source job")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum export duration")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) || *file == "" || *expected == "" {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-job-export APP SUB SOURCE_UUID --expected-created-at TIMESTAMP --file PATH [--outcomes unresolved,conflict,changed,unavailable] [--timeout 2m] [--raw]", "events")
		return 1
	}
	sub, subErr := uuid.Parse(positional[1])
	id, err := uuid.Parse(positional[2])
	created, timeErr := time.Parse(time.RFC3339Nano, *expected)
	if subErr != nil || err != nil || sub == uuid.Nil || id == uuid.Nil || timeErr != nil || created.IsZero() || *timeout <= 0 {
		return printErr("Invalid export guards", fmt.Errorf("subscription/request IDs must be nonzero UUIDs, creation timestamp must be RFC3339, and timeout must be positive"))
	}
	selectedOutcomes, err := lineageJobExportOutcomes(*outcomes)
	if err != nil {
		return printErr("Invalid outcome selection", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	return exportLineageRetryJob(ctx, client, positional[0], sub.String(), id.String(), created, *file, selectedOutcomes, *rawOutput)
}

func lineageJobExportOutcomes(raw string) (map[string]bool, error) {
	selected := make(map[string]bool)
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		switch value {
		case "unresolved", "conflict", "changed", "unavailable", "cancelled":
			selected[value] = true
		default:
			return nil, fmt.Errorf("outcomes must contain only unresolved, conflict, changed, unavailable or cancelled")
		}
	}
	return selected, nil
}

func readLineageJobExportSource(ctx context.Context, client *api.Client, app, sub, id string, expected time.Time) (api.EventExecutionLineageRetryJob, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	job, err := client.GetEventExecutionLineageRetryJob(readCtx, app, sub, id)
	if err != nil {
		return job, err
	}
	if !sameBindingDeployment(job.RequestID, id) || !sameBindingDeployment(job.SubscriptionID, sub) || !job.CreatedAt.Equal(expected) {
		return job, fmt.Errorf("source job identity changed; review its original creation timestamp")
	}
	if _, err := summarizeLineageRetryWatch(job); err != nil {
		return job, err
	}
	if job.Status != "completed" && job.Status != "cancelled" {
		return job, fmt.Errorf("source job must be completed or cancelled before exporting remaining cases")
	}
	seen := make(map[int64]bool)
	for _, item := range job.Items {
		c := item.Selection
		if c.OutboxID <= 0 || seen[c.OutboxID] || c.AcceptedAt.IsZero() || c.ExpectedRevision <= 0 {
			return job, fmt.Errorf("invalid source selection for case %d", c.OutboxID)
		}
		if c.ExpectedDiagnosticID != "" {
			diagnostic, err := uuid.Parse(c.ExpectedDiagnosticID)
			if err != nil || diagnostic == uuid.Nil {
				return job, fmt.Errorf("invalid source diagnostic identity for case %d", c.OutboxID)
			}
		}
		seen[c.OutboxID] = true
	}
	return job, nil
}

func currentLineageJobExportCase(ctx context.Context, client *api.Client, app, sub string, job api.EventExecutionLineageRetryJob, item api.EventExecutionLineageRetryJobItem) (*api.EventExecutionLineageCase, string, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	original := item.Selection
	// A one-row page seeks directly to this outbox ID without scanning unrelated cases.
	page, err := client.ListEventExecutionLineageCases(readCtx, app, sub, original.OutboxID-1, 1)
	if err != nil {
		return nil, "", err
	}
	if len(page.Cases) > 1 {
		return nil, "", fmt.Errorf("case lookup exceeded its one-row limit")
	}
	if len(page.Cases) == 0 {
		return nil, "unavailable", nil
	}
	c := page.Cases[0]
	if c.OutboxID < original.OutboxID || !sameBindingDeployment(c.SubscriptionID, sub) || !sameBindingDeployment(c.AppID, job.AppID) {
		return nil, "", fmt.Errorf("case lookup returned an invalid identity")
	}
	if c.OutboxID != original.OutboxID {
		return nil, "unavailable", nil
	}
	if !c.AcceptedAt.Equal(original.AcceptedAt) {
		return nil, "acceptance_changed", nil
	}
	if c.DiagnosticID != original.ExpectedDiagnosticID && !sameBindingDeployment(c.DiagnosticID, original.ExpectedDiagnosticID) {
		return nil, "diagnostic_changed", nil
	}
	switch c.Status {
	case "unresolved", "conflict":
		return &c, "", nil
	case "repaired", "already_associated":
		return nil, "no_longer_retryable", nil
	default:
		return nil, "", fmt.Errorf("case %d returned unsupported state %q", c.OutboxID, c.Status)
	}
}

func exportLineageRetryJob(ctx context.Context, client *api.Client, app, sub, id string, expected time.Time, file string, outcomes map[string]bool, rawOutput bool) int {
	source, err := readLineageJobExportSource(ctx, client, app, sub, id, expected)
	if err != nil {
		return printErr("Source lineage retry job unavailable", err)
	}
	summary := lineageJobExportSummary{SourceRequestID: id, SourceCreatedAt: source.CreatedAt, AppID: source.AppID, App: app, SubscriptionID: sub, SourceOutcomes: make(map[string]int), Reasons: make(map[string]int), Skipped: []lineageJobExportSkip{}}
	preview := api.EventExecutionLineageCases{Cases: []api.EventExecutionLineageCase{}}
	for _, item := range source.Items {
		if !outcomes[item.Status] {
			continue
		}
		summary.SourceOutcomes[item.Status]++
		current, reason, err := currentLineageJobExportCase(ctx, client, app, sub, source, item)
		if err != nil {
			return printErr("Current lineage case lookup failed; no request file written", err)
		}
		if current == nil {
			summary.Skipped = append(summary.Skipped, lineageJobExportSkip{OutboxID: item.Selection.OutboxID, SourceOutcome: item.Status, Reason: reason})
			continue
		}
		preview.Cases = append(preview.Cases, *current)
	}
	// Recheck retention and the original incarnation before creating a local request.
	latest, err := readLineageJobExportSource(ctx, client, app, sub, id, expected)
	if err != nil {
		return printErr("Source job recheck failed; no request file written", err)
	}
	if !sameBindingDeployment(latest.AppID, source.AppID) || !latest.RetainUntil.Equal(source.RetainUntil) || latest.Status != source.Status {
		return printErr("Source job changed; no request file written", fmt.Errorf("refresh and review the source job"))
	}
	if len(preview.Cases) == 0 {
		if code := renderLineageJobExportSummary(summary); code != 0 {
			return code
		}
		return printErr("No remaining retryable cases", fmt.Errorf("no request file written; inspect skipped cases and selected source outcomes"))
	}
	sort.Slice(preview.Cases, func(i, j int) bool { return preview.Cases[i].OutboxID < preview.Cases[j].OutboxID })
	req, reasons, err := lineageRetryExportRequest(preview, sub, 0, api.EventExecutionLineageRetryJobMaxCases, "")
	if err != nil {
		return printErr("Invalid current lineage guards; no request file written", err)
	}
	if err := ctx.Err(); err != nil {
		return printErr("Lineage job export stopped; no request file written", err)
	}
	summary.Format = lineageRetryBundleFormat
	if rawOutput {
		summary.Format = "raw"
		err = writeLineageRetryExport(file, req)
	} else {
		err = writeScopedLineageRetryExport(file, req, source.AppID, sub, &lineageRetryFileSource{RequestID: source.RequestID, CreatedAt: source.CreatedAt})
	}
	if err != nil {
		return printErr("Lineage job export failed", err)
	}
	summary.File, summary.RequestID, summary.Selected, summary.Reasons = file, req.RequestID, len(req.Cases), reasons
	return renderLineageJobExportSummary(summary)
}

func renderLineageJobExportSummary(summary lineageJobExportSummary) int {
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Source job %s | created %s | selected %d | skipped %d\n", summary.SourceRequestID, summary.SourceCreatedAt.Format(time.RFC3339Nano), summary.Selected, len(summary.Skipped))
	for _, item := range summary.Skipped {
		_, _ = fmt.Fprintf(osStdout, "Skipped case %d | original %s | %s\n", item.OutboxID, oneLine(item.SourceOutcome), oneLine(item.Reason))
	}
	if summary.File != "" {
		_, _ = fmt.Fprintf(osStdout, "Saved %s | %s | request %s | app %s (%s) | subscription %s\nReview before submitting with subscription-lineage-retry-job-create; reuse this file after a timeout.\n", oneLine(summary.File), summary.Format, summary.RequestID, oneLine(summary.App), summary.AppID, summary.SubscriptionID)
	}
	return 0
}
