package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
)

func cmdCustomerOperationAttention(args []string) int {
	return cmdCustomerOperationAttentionMode(args, false)
}
func cmdCustomerOperationAttentionSummary(args []string) int {
	return cmdCustomerOperationAttentionMode(args, true)
}
func cmdCustomerOperationAttentionMode(args []string, summary bool) int {
	fs := flag.NewFlagSet("customer-operations attention", flag.ContinueOnError)
	var app string
	var self bool
	var opts api.OperationWorkflowAttentionOptions
	var groupBy string
	if summary {
		fs.StringVar(&groupBy, "group-by", "workflow", "workflow, blocker_code, target_operation, customer, dependency_status, or required_outcome_code")
	}
	fs.StringVar(&opts.DependencyStatus, "dependency-status", "", "waiting, unknown, or outcome_mismatch")
	fs.StringVar(&opts.RequiredOutcomeCode, "required-outcome-code", "", "required prerequisite outcome filter")
	fs.StringVar(&opts.BlockerCode, "blocker-code", "", "blocker code filter")
	fs.StringVar(&app, "app", "", "owned application slug")
	fs.BoolVar(&self, "self", false, "authenticated customer queue")
	fs.StringVar(&opts.AppID, "app-id", "", "required application UUID in self mode")
	fs.StringVar(&opts.Scope, "scope", "", "explicit environment")
	fs.StringVar(&opts.TenantID, "tenant", "", "optional account customer filter")
	fs.StringVar(&opts.Workflow, "workflow", "", "workflow filter")
	fs.StringVar(&opts.TargetOperation, "target-operation", "", "target Operation filter")
	fs.StringVar(&opts.Reason, "reason", "", "blocked, stale, overdue, or dependency")
	fs.IntVar(&opts.Limit, "limit", api.OperationHistoryPageDefault, "page size")
	fs.StringVar(&opts.Cursor, "cursor", "", "continuation cursor")
	if err := fs.Parse(args); err != nil {
		return printErr("Invalid attention command", err)
	}
	if fs.NArg() != 0 || opts.Scope == "" || self && (app != "" || opts.AppID == "" || opts.TenantID != "") || !self && (app == "" || opts.AppID != "") {
		return printErr("Invalid attention command", fmt.Errorf("use --app SLUG --scope SCOPE, or --self --app-id UUID --scope SCOPE"))
	}
	if api.ValidateScope(opts.Scope) != nil || opts.Limit < 1 || opts.Limit > api.OperationHistoryPageMax || len(opts.Cursor) > api.OperationHistoryCursorMaxBytes || opts.Workflow != "" && api.ValidateOperationWorkflowName(opts.Workflow) != nil || opts.Reason != "" && opts.Reason != "blocked" && opts.Reason != "stale" && opts.Reason != "overdue" && opts.Reason != "dependency" {
		return printErr("Invalid attention command", fmt.Errorf("invalid environment, workflow, reason, page size, or cursor"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if summary {
		var result api.OperationWorkflowAttentionSummary
		options := api.OperationWorkflowAttentionSummaryOptions{OperationWorkflowAttentionOptions: opts, GroupBy: groupBy}
		if self {
			result, err = client.SummarizePlatformTenantSelfWorkflowAttention(ctx, options)
		} else {
			result, err = client.SummarizeAccountWorkflowAttention(ctx, app, options)
		}
		if err != nil {
			return printErr("Workflow summary failed", err)
		}
		if jsonOutput {
			err = json.NewEncoder(osStdout).Encode(result)
		} else {
			_, err = fmt.Fprintf(osStdout, "Evaluated at: %s\nTotals: workflows=%d blocked=%d stale=%d overdue=%d blockers=%d unknown-age=%d dependency-workflows=%d dependencies=%d\n", result.EvaluatedAt.Format(time.RFC3339), result.Totals.WorkflowCount, result.Totals.BlockedWorkflowCount, result.Totals.StaleWorkflowCount, result.Totals.OverdueWorkflowCount, result.Totals.BlockerCount, result.Totals.UnknownAgeBlockers, result.Totals.DependencyWorkflowCount, result.Totals.DependencyCount)
			if err == nil && result.Totals.LongestOverdueSeconds != nil {
				_, err = fmt.Fprintf(osStdout, "Oldest missed deadline: %s (%d seconds overdue)\n", result.Totals.EarliestOverdueDeadlineAt.Format(time.RFC3339Nano), *result.Totals.LongestOverdueSeconds)
			}
			for _, g := range result.Groups {
				if err != nil {
					break
				}
				age := "unknown"
				if g.Stats.OldestBlockerAgeSeconds != nil {
					age = fmt.Sprintf("%ds", *g.Stats.OldestBlockerAgeSeconds)
				}
				_, err = fmt.Fprintf(osStdout, "%s\tworkflows=%d\tblocked=%d\tstale=%d\toverdue=%d\tblockers=%d\tunknown-age=%d\toldest=%s\tdependency-workflows=%d\tdependencies=%d\n", g.Value, g.Stats.WorkflowCount, g.Stats.BlockedWorkflowCount, g.Stats.StaleWorkflowCount, g.Stats.OverdueWorkflowCount, g.Stats.BlockerCount, g.Stats.UnknownAgeBlockers, age, g.Stats.DependencyWorkflowCount, g.Stats.DependencyCount)
			}
			if err == nil && result.NextCursor != "" {
				_, err = fmt.Fprintf(osStdout, "Next page: --cursor %s\n", result.NextCursor)
			}
		}
		if err != nil {
			return printErr("Print attention summary", err)
		}
		return 0
	}
	var page api.OperationWorkflowAttentionResponse
	if self {
		page, err = client.ListPlatformTenantSelfWorkflowAttention(ctx, opts)
	} else {
		page, err = client.ListAccountWorkflowAttention(ctx, app, opts)
	}
	if err != nil {
		return printErr("Workflow attention failed", err)
	}
	if err = printCustomerOperationAttention(osStdout, page, jsonOutput); err != nil {
		return printErr("Print attention queue", err)
	}
	return 0
}
func printCustomerOperationAttention(out io.Writer, page api.OperationWorkflowAttentionResponse, jsonMode bool) error {
	if jsonMode {
		return json.NewEncoder(out).Encode(page)
	}
	if _, err := fmt.Fprintf(out, "Evaluated at: %s\n", page.EvaluatedAt.UTC().Format("2006-01-02T15:04:05.999999Z")); err != nil {
		return err
	}
	for _, item := range page.Items {
		if _, err := fmt.Fprintf(out, "%s\tinstance=%s\tsubject=%s:%s\ttenant=%s\tstate=%s\trevision=%d\treasons=%s\toperation=%s\n", item.State.Workflow, item.State.InstanceID, item.Subject.Type, item.Subject.ID, item.PlatformTenantID, item.State.State, item.State.Revision, strings.Join(item.Reasons, ","), item.OperationID); err != nil {
			return err
		}
		if item.State.DeadlineAt != "" {
			if _, err := fmt.Fprintf(out, "  deadline\tdue_at=%s\toverdue=%t\toverdue_seconds=%d\n", item.State.DeadlineAt, item.State.Overdue, item.State.OverdueSeconds); err != nil {
				return err
			}
		}
		for _, dep := range item.DependencyAttention {
			if _, err := fmt.Fprintf(out, "  dependency\tsubject=%s:%s\tworkflow=%s\tinstance=%s\tstatus=%s\trequired-outcome=%s\n", dep.Dependency.SubjectType, dep.Dependency.SubjectID, dep.Dependency.Workflow, dep.Dependency.InstanceID, dep.Status, dep.Dependency.RequiredOutcomeCode); err != nil {
				return err
			}
		}
		for _, b := range item.State.Blockers {
			if _, err := fmt.Fprintf(out, "  blocker\toperation=%s\tcode=%s\tfirst_observed_at=%s\t%s\n", b.Operation, b.Code, b.FirstObservedAt, b.Description); err != nil {
				return err
			}
		}
	}
	if len(page.Items) == 0 {
		if _, err := fmt.Fprintln(out, "No retained workflows need attention for these filters."); err != nil {
			return err
		}
	}
	if page.NextCursor != "" {
		_, err := fmt.Fprintf(out, "Next page: --cursor %s\n", page.NextCursor)
		return err
	}
	return nil
}
