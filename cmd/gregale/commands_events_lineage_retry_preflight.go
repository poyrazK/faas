package main

import (
	"context"
	"errors"
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

type lineageRetryPreflightCase struct {
	Selection api.EventExecutionLineageRetrySelection `json:"selection"`
	Status    string                                  `json:"status"`
	Issues    []string                                `json:"issues"`
	Current   *api.EventExecutionLineageCase          `json:"current,omitempty"`
}

type lineageRetryPreflightReport struct {
	Scope          *lineageRetryFileScope                    `json:"scope,omitempty"`
	SourceJob      *lineageRetryFileSource                   `json:"source_job,omitempty"`
	File           string                                    `json:"file"`
	App            string                                    `json:"app"`
	SubscriptionID string                                    `json:"subscription_id"`
	RequestID      string                                    `json:"request_id"`
	Complete       bool                                      `json:"complete"`
	Ready          bool                                      `json:"ready"`
	Total          int                                       `json:"total"`
	Checked        int                                       `json:"checked"`
	ReadyCases     int                                       `json:"ready_cases"`
	RequestState   string                                    `json:"request_state"`
	ExistingJob    *api.EventExecutionLineageRetryJobSummary `json:"existing_job,omitempty"`
	Cases          []lineageRetryPreflightCase               `json:"cases"`
}

func cmdEventsLineageRetryPreflight(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-preflight", flag.ContinueOnError)
	file := fs.String("file", "", "saved JSON request path")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum preflight duration")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *file == "" {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-preflight APP SUB --file PATH [--timeout 2m]", "events")
		return 1
	}
	sub, err := uuid.Parse(positional[1])
	if err != nil || sub == uuid.Nil || *timeout <= 0 {
		return printErr("Invalid preflight options", fmt.Errorf("subscription must be a nonzero UUID and timeout must be positive"))
	}
	input, err := readLineageRetryRequestFile(*file)
	if err == nil {
		err = input.validateSubscription(sub.String())
	}
	if err != nil {
		return printErr("Invalid retry request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	if err := input.validateApp(ctx, client, positional[0]); err != nil {
		return printErr("Retry file app target mismatch", err)
	}
	return preflightLineageRetryRequest(ctx, client, positional[0], sub.String(), *file, input)
}

// Normalize in memory only, matching submission's UUID, ordering and timestamp semantics.
func normalizeLineagePreflightRequest(req api.CreateEventExecutionLineageRetryJobRequest) (api.CreateEventExecutionLineageRetryJobRequest, error) {
	id, err := uuid.Parse(req.RequestID)
	if err != nil || id == uuid.Nil || len(req.Cases) < 1 || len(req.Cases) > api.EventExecutionLineageRetryJobMaxCases {
		return req, fmt.Errorf("request must have a nonzero UUID and 1 to %d cases", api.EventExecutionLineageRetryJobMaxCases)
	}
	req.RequestID = id.String()
	req.Cases = append([]api.EventExecutionLineageRetrySelection(nil), req.Cases...)
	sort.Slice(req.Cases, func(i, j int) bool { return req.Cases[i].OutboxID < req.Cases[j].OutboxID })
	for i, c := range req.Cases {
		if c.OutboxID <= 0 || c.ExpectedRevision <= 0 || c.AcceptedAt.IsZero() || (i > 0 && req.Cases[i-1].OutboxID == c.OutboxID) {
			return req, fmt.Errorf("invalid or duplicate selection for case %d", c.OutboxID)
		}
		if c.ExpectedDiagnosticID != "" {
			diagnostic, err := uuid.Parse(c.ExpectedDiagnosticID)
			if err != nil || diagnostic == uuid.Nil {
				return req, fmt.Errorf("invalid diagnostic guard for case %d", c.OutboxID)
			}
			req.Cases[i].ExpectedDiagnosticID = diagnostic.String()
		}
		req.Cases[i].AcceptedAt = c.AcceptedAt.UTC()
	}
	return req, nil
}

func readLineagePreflightJob(ctx context.Context, client *api.Client, app, sub, id string) (*api.EventExecutionLineageRetryJob, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	job, err := client.GetEventExecutionLineageRetryJob(readCtx, app, sub, id)
	var problem *api.APIError
	if errors.As(err, &problem) && problem.Problem.Status == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !sameBindingDeployment(job.RequestID, id) || !sameBindingDeployment(job.SubscriptionID, sub) {
		return nil, fmt.Errorf("job lookup returned another request or subscription")
	}
	if _, err := summarizeLineageRetryWatch(job); err != nil {
		return nil, err
	}
	return &job, nil
}

func checkLineagePreflightCase(ctx context.Context, client *api.Client, app, sub string, selection api.EventExecutionLineageRetrySelection) (lineageRetryPreflightCase, error) {
	result := lineageRetryPreflightCase{Selection: selection, Status: "unavailable", Issues: []string{"unavailable"}}
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	page, err := client.ListEventExecutionLineageCases(readCtx, app, sub, selection.OutboxID-1, 1)
	if err != nil {
		return result, err
	}
	if len(page.Cases) > 1 {
		return result, fmt.Errorf("case lookup exceeded its one-row limit")
	}
	if len(page.Cases) == 0 {
		return result, nil
	}
	current := page.Cases[0]
	appID, err := uuid.Parse(current.AppID)
	if err != nil || appID == uuid.Nil || !sameBindingDeployment(current.SubscriptionID, sub) || current.OutboxID < selection.OutboxID {
		return result, fmt.Errorf("case lookup returned an invalid identity")
	}
	if current.OutboxID != selection.OutboxID {
		return result, nil
	}
	if current.Revision <= 0 || current.AcceptedAt.IsZero() {
		return result, fmt.Errorf("case %d returned invalid guards", current.OutboxID)
	}
	if current.DiagnosticID != "" {
		diagnostic, err := uuid.Parse(current.DiagnosticID)
		if err != nil || diagnostic == uuid.Nil {
			return result, fmt.Errorf("case %d returned invalid diagnostic identity", current.OutboxID)
		}
		current.DiagnosticID = diagnostic.String()
	}
	result.Current = &current
	result.Status, result.Issues = "ready", []string{}
	if !current.AcceptedAt.Equal(selection.AcceptedAt) {
		result.Issues = append(result.Issues, "acceptance_changed")
	}
	if current.DiagnosticID != selection.ExpectedDiagnosticID {
		result.Issues = append(result.Issues, "diagnostic_changed")
	}
	if current.Revision != selection.ExpectedRevision {
		result.Issues = append(result.Issues, "revision_changed")
	}
	switch current.Status {
	case "unresolved", "conflict":
	case "repaired", "already_associated":
		result.Issues = append(result.Issues, "no_longer_retryable")
	default:
		return result, fmt.Errorf("case %d returned unsupported state %q", current.OutboxID, current.Status)
	}
	if len(result.Issues) > 0 {
		result.Status = result.Issues[0]
	}
	return result, nil
}

func lineagePreflightJobMatches(job api.EventExecutionLineageRetryJob, req api.CreateEventExecutionLineageRetryJobRequest) (bool, error) {
	actual := api.CreateEventExecutionLineageRetryJobRequest{RequestID: job.RequestID}
	for _, item := range job.Items {
		actual.Cases = append(actual.Cases, item.Selection)
	}
	actual, err := normalizeLineagePreflightRequest(actual)
	if err != nil {
		return false, err
	}
	if actual.RequestID != req.RequestID || len(actual.Cases) != len(req.Cases) {
		return false, nil
	}
	for i, c := range actual.Cases {
		expected := req.Cases[i]
		if c.OutboxID != expected.OutboxID || c.ExpectedRevision != expected.ExpectedRevision || c.ExpectedDiagnosticID != expected.ExpectedDiagnosticID || !c.AcceptedAt.Equal(expected.AcceptedAt) {
			return false, nil
		}
	}
	return true, nil
}

func preflightLineageRetryRequest(ctx context.Context, client *api.Client, app, sub, file string, input lineageRetryRequestFile) int {
	req := input.Request
	report := lineageRetryPreflightReport{Scope: input.Scope, SourceJob: input.SourceJob, File: file, App: app, SubscriptionID: sub, RequestID: req.RequestID, Total: len(req.Cases), RequestState: "unverified", Cases: []lineageRetryPreflightCase{}}
	first, err := readLineagePreflightJob(ctx, client, app, sub, req.RequestID)
	if err != nil {
		return failLineagePreflight(report, err)
	}
	// Pin the app identity observed through these subscription-scoped reads.
	appID := ""
	if input.Scope != nil {
		appID = input.Scope.AppID
	}
	if first != nil {
		if appID != "" && !sameBindingDeployment(first.AppID, appID) {
			return failLineagePreflight(report, fmt.Errorf("job app identity differs from scoped retry file"))
		}
		appID = first.AppID
	}
	for _, selection := range req.Cases {
		result, err := checkLineagePreflightCase(ctx, client, app, sub, selection)
		if err != nil {
			return failLineagePreflight(report, err)
		}
		if result.Current != nil {
			if appID != "" && !sameBindingDeployment(result.Current.AppID, appID) {
				return failLineagePreflight(report, fmt.Errorf("app identity changed during case checks"))
			}
			appID = result.Current.AppID
		}
		report.Cases = append(report.Cases, result)
		report.Checked++
		if result.Status == "ready" {
			report.ReadyCases++
		}
	}
	latest, err := readLineagePreflightJob(ctx, client, app, sub, req.RequestID)
	if err != nil {
		return failLineagePreflight(report, err)
	}
	report.RequestState = "not_retained"
	report.Ready = report.ReadyCases == report.Total
	if first != nil && latest == nil {
		report.RequestState = "identity_unavailable"
		report.Ready = false
	}
	if latest != nil {
		report.ExistingJob = &api.EventExecutionLineageRetryJobSummary{RequestID: latest.RequestID, SubscriptionID: latest.SubscriptionID, AppID: latest.AppID, Status: latest.Status, Selected: len(latest.Items), Processed: latest.Processed, CreatedAt: latest.CreatedAt, UpdatedAt: latest.UpdatedAt, RetainUntil: latest.RetainUntil}
		matches, err := lineagePreflightJobMatches(*latest, req)
		if err != nil {
			return failLineagePreflight(report, err)
		}
		report.RequestState, report.Ready = "identity_conflict", false
		if matches {
			report.RequestState, report.Ready = "already_submitted", true
		}
		if (appID != "" && !sameBindingDeployment(latest.AppID, appID)) || (first != nil && (!latest.CreatedAt.Equal(first.CreatedAt) || !latest.RetainUntil.Equal(first.RetainUntil))) {
			report.RequestState, report.Ready = "identity_changed", false
		}
	}
	if err := ctx.Err(); err != nil {
		return failLineagePreflight(report, err)
	}
	report.Complete = true
	if code := renderLineagePreflight(report); code != 0 {
		return code
	}
	if !report.Ready {
		return 1
	}
	return 0
}

func failLineagePreflight(report lineageRetryPreflightReport, err error) int {
	report.Complete, report.Ready = false, false
	if code := renderLineagePreflight(report); code != 0 {
		return code
	}
	return printErr("Lineage retry preflight incomplete", err)
}

func renderLineagePreflight(report lineageRetryPreflightReport) int {
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Request %s | %s | ready %t | checked %d/%d | ready cases %d\n", report.RequestID, report.RequestState, report.Ready, report.Checked, report.Total, report.ReadyCases)
	if job := report.ExistingJob; job != nil {
		_, _ = fmt.Fprintf(osStdout, "Existing job %s | processed %d/%d | created %s | expires %s\n", oneLine(job.Status), job.Processed, job.Selected, job.CreatedAt.Format(time.RFC3339Nano), job.RetainUntil.Format(time.RFC3339Nano))
	}
	if report.RequestState == "already_submitted" {
		_, _ = fmt.Fprintln(osStdout, "Repeated submission returns this job and its recorded progress.")
	}
	for _, item := range report.Cases {
		_, _ = fmt.Fprintf(osStdout, "Case %d | %s", item.Selection.OutboxID, item.Status)
		if len(item.Issues) > 0 {
			_, _ = fmt.Fprintf(osStdout, " | %s", strings.Join(item.Issues, ", "))
		}
		if item.Current != nil {
			_, _ = fmt.Fprintf(osStdout, " | expected revision %d | current revision %d", item.Selection.ExpectedRevision, item.Current.Revision)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	_, _ = fmt.Fprintln(osStdout, "Preflight is advisory; submission validates current state. The request file and UUID are preserved.")
	return 0
}
