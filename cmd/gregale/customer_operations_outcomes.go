package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"os/signal"
	"time"
)

func cmdCustomerOperationOutcomes(args []string, summary bool) int {
	fs := newFlagSet("customer-operations outcomes", flag.ContinueOnError)
	var app, group string
	var self bool
	var opts api.OperationWorkflowOutcomeOptions
	fs.StringVar(&app, "app", "", "owned application slug")
	fs.BoolVar(&self, "self", false, "authenticated customer")
	fs.StringVar(&opts.AppID, "app-id", "", "required application UUID with --self")
	fs.StringVar(&opts.Scope, "scope", "", "explicit environment")
	fs.StringVar(&opts.TenantID, "tenant", "", "account customer filter")
	fs.StringVar(&opts.Workflow, "workflow", "", "workflow filter")
	fs.StringVar(&opts.Code, "code", "", "outcome code filter")
	fs.IntVar(&opts.Limit, "limit", 20, "page size 1–100")
	fs.StringVar(&opts.Cursor, "cursor", "", "continuation cursor")
	if summary {
		fs.StringVar(&group, "group-by", "outcome", "outcome, workflow, or account-only customer")
	}
	if err := fs.Parse(args); err != nil {
		return printErr("Invalid outcome command", err)
	}
	if fs.NArg() != 0 || opts.Scope == "" || self && (app != "" || opts.AppID == "" || opts.TenantID != "") || !self && (app == "" || opts.AppID != "") || api.ValidateScope(opts.Scope) != nil || opts.Limit < 1 || opts.Limit > 100 || len(opts.Cursor) > 512 || opts.Workflow != "" && api.ValidateOperationWorkflowName(opts.Workflow) != nil {
		return printErr("Invalid outcome command", fmt.Errorf("use --app SLUG --scope SCOPE, or --self --app-id UUID --scope SCOPE with valid selectors"))
	}
	if summary && (group != "outcome" && group != "workflow" && group != "customer" || self && group == "customer") {
		return printErr("Invalid outcome grouping", fmt.Errorf("choose outcome, workflow, or account-only customer"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if summary {
		var out api.OperationWorkflowOutcomeSummary
		options := api.OperationWorkflowOutcomeSummaryOptions{OperationWorkflowOutcomeOptions: opts, GroupBy: group}
		if self {
			out, err = client.SummarizePlatformTenantSelfWorkflowOutcomes(ctx, options)
		} else {
			out, err = client.SummarizeAccountWorkflowOutcomes(ctx, app, options)
		}
		if err != nil {
			return printErr("Workflow outcome summary failed", err)
		}
		if jsonOutput {
			err = json.NewEncoder(osStdout).Encode(out)
		} else {
			_, err = fmt.Fprintf(osStdout, "Evaluated at: %s\nCompleted workflows with outcomes: %d\n", out.EvaluatedAt.Format(time.RFC3339Nano), out.WorkflowCount)
			for _, g := range out.Groups {
				if err != nil {
					break
				}
				_, err = fmt.Fprintf(osStdout, "%s\tworkflows=%d\n", g.Value, g.WorkflowCount)
			}
			if err == nil && out.NextCursor != "" {
				_, err = fmt.Fprintf(osStdout, "Next page: --cursor %s\n", out.NextCursor)
			}
		}
	} else {
		var out api.OperationWorkflowOutcomesResponse
		if self {
			out, err = client.ListPlatformTenantSelfWorkflowOutcomes(ctx, opts)
		} else {
			out, err = client.ListAccountWorkflowOutcomes(ctx, app, opts)
		}
		if err != nil {
			return printErr("Workflow outcomes failed", err)
		}
		if jsonOutput {
			err = json.NewEncoder(osStdout).Encode(out)
		} else {
			_, err = fmt.Fprintf(osStdout, "Evaluated at: %s\n", out.EvaluatedAt.Format(time.RFC3339Nano))
			for _, e := range out.Items {
				if err != nil {
					break
				}
				_, err = fmt.Fprintf(osStdout, "%s\tinstance=%s\tsubject=%s:%s\ttenant=%s\tstate=%s\toutcome=%s\trevision=%d\treport=%s\toperation=%s\t%s\n", e.State.Workflow, e.State.InstanceID, e.Subject.Type, e.Subject.ID, e.PlatformTenantID, e.State.State, e.State.OutcomeCode, e.State.Revision, e.State.ReportID, e.OperationID, e.State.OutcomeDescription)
			}
			if err == nil && out.NextCursor != "" {
				_, err = fmt.Fprintf(osStdout, "Next page: --cursor %s\n", out.NextCursor)
			}
		}
	}
	if err != nil {
		return printErr("Print workflow outcomes", err)
	}
	return 0
}
