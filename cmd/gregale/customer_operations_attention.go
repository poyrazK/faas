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
	fs := newFlagSet("customer-operations attention", flag.ContinueOnError)
	var app string
	var self bool
	var opts api.OperationWorkflowAttentionOptions
	var groupBy string
	if summary {
		fs.StringVar(&groupBy, "group-by", "workflow", "workflow, owner, blocker_code, target_operation, customer, dependency_status, or required_outcome_code")
	}
	fs.StringVar(&opts.DependencyStatus, "dependency-status", "", "waiting, unknown, or outcome_mismatch")
	fs.StringVar(&opts.RequiredOutcomeCode, "required-outcome-code", "", "required prerequisite outcome filter")
	fs.StringVar(&opts.Priority, "priority", "", "low, normal, high, or urgent blocker priority")
	fs.StringVar(&opts.Sort, "sort", "updated_at", "updated_at or deadline queue order")
	fs.StringVar(&opts.Owner, "owner", "", "application-assigned blocker owner")
	fs.BoolVar(&opts.Unassigned, "unassigned", false, "only blockers without an owner")
	fs.StringVar(&opts.BlockerCode, "blocker-code", "", "blocker code filter")
	fs.StringVar(&app, "app", "", "owned application slug")
	fs.BoolVar(&self, "self", false, "authenticated customer queue")
	fs.StringVar(&opts.AppID, "app-id", "", "required application UUID in self mode")
	fs.StringVar(&opts.Scope, "scope", "", "explicit environment")
	fs.StringVar(&opts.TenantID, "tenant", "", "optional account customer filter")
	fs.StringVar(&opts.Workflow, "workflow", "", "workflow filter")
	fs.StringVar(&opts.TargetOperation, "target-operation", "", "target Operation filter")
	fs.StringVar(&opts.Reason, "reason", "", "blocked, stale, overdue, dependency, escalated, unacknowledged, follow_up_overdue, awaiting_verification, sla_breached, or sla_at_risk")
	fs.IntVar(&opts.Limit, "limit", api.OperationHistoryPageDefault, "page size")
	fs.StringVar(&opts.Cursor, "cursor", "", "continuation cursor")
	if err := fs.Parse(args); err != nil {
		return printErr("Invalid attention command", err)
	}
	if opts.Owner != "" && opts.Unassigned {
		return printErr("Invalid attention command", fmt.Errorf("choose --owner or --unassigned"))
	}
	if fs.NArg() != 0 || opts.Scope == "" || self && (app != "" || opts.AppID == "" || opts.TenantID != "") || !self && (app == "" || opts.AppID != "") {
		return printErr("Invalid attention command", fmt.Errorf("use --app SLUG --scope SCOPE, or --self --app-id UUID --scope SCOPE"))
	}
	if api.ValidateScope(opts.Scope) != nil || opts.Limit < 1 || opts.Limit > api.OperationHistoryPageMax || len(opts.Cursor) > api.OperationHistoryCursorMaxBytes || opts.Workflow != "" && api.ValidateOperationWorkflowName(opts.Workflow) != nil || opts.Reason != "" && opts.Reason != "blocked" && opts.Reason != "stale" && opts.Reason != "overdue" && opts.Reason != "dependency" && opts.Reason != "escalated" && opts.Reason != "unacknowledged" && opts.Reason != "follow_up_overdue" && opts.Reason != "awaiting_verification" && opts.Reason != "sla_breached" && opts.Reason != "sla_at_risk" || opts.Priority != "" && opts.Priority != "low" && opts.Priority != "normal" && opts.Priority != "high" && opts.Priority != "urgent" || opts.Sort != "updated_at" && opts.Sort != "deadline" {
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
			if err == nil {
				_, err = fmt.Fprintf(osStdout, "SLA totals: at-risk-workflows=%d breached-workflows=%d unknown-workflows=%d\n", result.Totals.SLAAtRiskWorkflowCount, result.Totals.SLABreachedWorkflowCount, result.Totals.SLAUnknownWorkflowCount)
			}
			for _, g := range result.Groups {
				if err != nil {
					break
				}
				age := "unknown"
				if g.Stats.OldestBlockerAgeSeconds != nil {
					age = fmt.Sprintf("%ds", *g.Stats.OldestBlockerAgeSeconds)
				}
				label := g.Value
				if result.GroupBy == "owner" && label == "" {
					label = "(unassigned)"
				}
				escalationSuffix := ""
				if g.Stats.EscalatedBlockerCount > 0 {
					escalationSuffix = fmt.Sprintf("\tescalated-workflows=%d\tescalated-blockers=%d", g.Stats.EscalatedWorkflowCount, g.Stats.EscalatedBlockerCount)
				}
				_, err = fmt.Fprintf(osStdout, "%s\tworkflows=%d\tblocked=%d\tstale=%d\toverdue=%d\tblockers=%d\tunknown-age=%d\toldest=%s\tdependency-workflows=%d\tdependencies=%d%s\n", label, g.Stats.WorkflowCount, g.Stats.BlockedWorkflowCount, g.Stats.StaleWorkflowCount, g.Stats.OverdueWorkflowCount, g.Stats.BlockerCount, g.Stats.UnknownAgeBlockers, age, g.Stats.DependencyWorkflowCount, g.Stats.DependencyCount, escalationSuffix+fmt.Sprintf("\tsla-at-risk-workflows=%d\tsla-breached-workflows=%d\tsla-unknown-workflows=%d", g.Stats.SLAAtRiskWorkflowCount, g.Stats.SLABreachedWorkflowCount, g.Stats.SLAUnknownWorkflowCount)+fmt.Sprintf("\tunacknowledged-blockers=%d\tfollow-up-overdue-blockers=%d", g.Stats.UnacknowledgedBlockerCount, g.Stats.FollowUpOverdueBlockerCount)+fmt.Sprintf("\tpriority-low=%d\tpriority-normal=%d\tpriority-high=%d\tpriority-urgent=%d", g.Stats.LowBlockerCount, g.Stats.NormalBlockerCount, g.Stats.HighBlockerCount, g.Stats.UrgentBlockerCount)+fmt.Sprintf("\tawaiting-verification-workflows=%d\tawaiting-verification-resolutions=%d", g.Stats.AwaitingVerificationWorkflowCount, g.Stats.AwaitingVerificationResolutionCount))
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
		if err := printWorkflowSLA(out, item.State.SLA); err != nil {
			return err
		}
		for _, dep := range item.DependencyAttention {
			if _, err := fmt.Fprintf(out, "  dependency\tsubject=%s:%s\tworkflow=%s\tinstance=%s\tstatus=%s\trequired-outcome=%s\n", dep.Dependency.SubjectType, dep.Dependency.SubjectID, dep.Dependency.Workflow, dep.Dependency.InstanceID, dep.Status, dep.Dependency.RequiredOutcomeCode); err != nil {
				return err
			}
		}
		if err := printResolutionVerifications(out, item.ResolutionVerifications, item.AwaitingVerificationCount, item.ResolutionVerificationCount); err != nil {
			return err
		}
		for _, e := range item.Escalations {
			if _, err := fmt.Fprintf(out, "  escalation\toperation=%s\tcode=%s\trecommended_owner=%s\tafter_seconds=%d\tescalated_at=%s\n", e.Operation, e.Code, e.Owner, e.AfterSeconds, e.EscalatedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		for _, b := range item.State.Blockers {
			if _, err := fmt.Fprintf(out, "  blocker\toperation=%s\tcode=%s\tfirst_observed_at=%s\t%s\n", b.Operation, b.Code, b.FirstObservedAt, b.Description); err != nil {
				return err
			}
			priority := b.Priority
			if priority == "" {
				priority = "normal"
			}
			if _, err := fmt.Fprintf(out, "    business-impact\tpriority=%s\t%s\n", priority, b.BusinessImpact); err != nil {
				return err
			}
			if b.AcknowledgedAt != "" || b.FollowUpAt != "" {
				if _, err := fmt.Fprintf(out, "    acknowledgement\tby=%s\tat=%s\tfollow_up_at=%s\n", b.AcknowledgedBy, b.AcknowledgedAt, b.FollowUpAt); err != nil {
					return err
				}
			}
			if b.Owner != "" || b.NextAction != "" {
				if _, err := fmt.Fprintf(out, "    assignment\towner=%s\tnext_action=%s\n", b.Owner, b.NextAction); err != nil {
					return err
				}
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

func printResolutionVerifications(out io.Writer, findings []api.OperationWorkflowResolutionVerification, pending, total int64) error {
	if total == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "  resolution-verification\tpending=%d\ttotal=%d\tpreview=%d\n", pending, total, len(findings)); err != nil {
		return err
	}
	for _, v := range findings {
		if _, err := fmt.Fprintf(out, "    resolution\toperation=%s\tcode=%s\tstatus=%s\towner=%s\tproof=%s:%s\n", v.Resolution.Operation, v.Resolution.Code, v.Status, v.Resolution.VerificationOwner, v.Resolution.VerificationMilestoneName, v.Resolution.VerificationMilestoneID); err != nil {
			return err
		}
	}
	return nil
}

func printWorkflowSLA(out io.Writer, sla *api.OperationWorkflowStateSLA) error {
	if sla == nil {
		return nil
	}
	if _, err := fmt.Fprintf(out, "workflow-sla\tstatus=%s\tbudget-seconds=%d\thistory-complete=%t\n", sla.Status, sla.BudgetSeconds, sla.HistoryComplete); err != nil {
		return err
	}
	if sla.WarningPercent != nil {
		warningAt := "unknown"
		if sla.WarningAt != nil {
			warningAt = sla.WarningAt.Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(out, "workflow-sla-warning\tpercent=%d\tat=%s\n", *sla.WarningPercent, warningAt); err != nil {
			return err
		}
	}
	if sla.ElapsedSeconds != nil && sla.RemainingSeconds != nil && sla.BreachedSeconds != nil && sla.DueAt != nil {
		_, err := fmt.Fprintf(out, "workflow-sla-time\telapsed-seconds=%d\tremaining-seconds=%d\tbreached-seconds=%d\tdue=%s\n", *sla.ElapsedSeconds, *sla.RemainingSeconds, *sla.BreachedSeconds, sla.DueAt.Format(time.RFC3339))
		return err
	}
	return nil
}
