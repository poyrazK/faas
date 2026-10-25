package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func cmdBillingBudgets(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if _, err := fmt.Fprintln(osStdout, "usage: gregale billing budgets <list|get|create|update|delete|history|channels> [flags] [ID]\nchannels ID lists the notification channels a budget's alerts also go to; --set ID,ID replaces them and --clear removes them.\nEnable monitored notify policies or compute-only stop_previews policies. Strict supports app/job gross compute or account net compute optionally combined with egress, using suspend_workloads with zero drain and no alert thresholds. Other stopping actions remain drafts. Preview consequences with billing budget-preview."); err != nil {
			return printErr("Could not print budget help", err)
		}
		return 0
	}
	verb := args[0]
	if verb != "list" && verb != "get" && verb != "create" && verb != "update" && verb != "delete" && verb != "history" && verb != "channels" {
		return printErr("Invalid budget command", errors.New("expected list, get, create, update, delete, history or channels"))
	}
	fs := newFlagSet("billing budgets "+verb, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable data")
	var file, key string
	var expected, after int64
	limit := api.FinancialBudgetHistoryMax
	if verb == "create" || verb == "update" {
		fs.StringVar(&file, "file", "", "budget spec JSON file")
	}
	if verb == "update" || verb == "delete" {
		fs.Int64Var(&expected, "expected-revision", 0, "current policy revision (required)")
	}
	if verb == "create" || verb == "update" || verb == "delete" {
		fs.StringVar(&key, "key", "", "stable operation key for retries")
	}
	var set string
	var clear bool
	if verb == "channels" {
		fs.StringVar(&set, "set", "", "replace the channels with these comma-separated channel IDs")
		fs.BoolVar(&clear, "clear", false, "remove every channel; alerts go to the account email only")
	}
	if verb == "history" {
		fs.Int64Var(&after, "after-revision", 0, "continue after this revision")
		fs.IntVar(&limit, "limit", api.FinancialBudgetHistoryMax, "maximum revisions in this page")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	id := ""
	needsID := verb == "get" || verb == "update" || verb == "delete" || verb == "history" || verb == "channels"
	if needsID {
		if fs.NArg() != 1 || uuid.Validate(fs.Arg(0)) != nil {
			return printErr("Invalid budget ID", errors.New("expected one UUID after flags"))
		}
		id = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return printErr("Invalid budget arguments", errors.New("unexpected positional argument"))
	}
	if (verb == "update" || verb == "delete") && (expected < 1 || expected >= api.FinancialBudgetRevisionMax) {
		return printErr("Invalid budget revision", errors.New("--expected-revision must be the current policy revision"))
	}
	if after < 0 || after > api.FinancialBudgetRevisionMax || limit < 1 || limit > api.FinancialBudgetHistoryMax || len(key) > api.FinancialBudgetOperationKeyBytes {
		return printErr("Invalid budget arguments", errors.New("history bounds or operation key exceeded"))
	}
	if verb == "channels" {
		return runBillingBudgetChannels(id, set, clear, *asJSON || jsonOutput)
	}
	// The global --json is consumed before dispatch (Hunt 12 H12-42).
	return runBillingBudget(verb, id, file, key, expected, after, limit, *asJSON || jsonOutput)
}

// runBillingBudgetChannels shows or replaces a budget's alert channels
// (ADR-20261019193500).
func runBillingBudgetChannels(id, set string, clear, asJSON bool) int {
	if clear && set != "" {
		return printErr("Invalid budget channels", errors.New("use --set or --clear, not both"))
	}
	var ids []string
	if set != "" {
		for _, raw := range strings.Split(set, ",") {
			raw = strings.TrimSpace(raw)
			if uuid.Validate(raw) != nil {
				return printErr("Invalid channel ID", fmt.Errorf("%q is not a UUID", raw))
			}
			ids = append(ids, raw)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.FinancialBudgetChannelsResponse
	if set != "" || clear {
		out, err = client.SetFinancialBudgetChannels(context.Background(), id, ids)
	} else {
		out, err = client.GetFinancialBudgetChannels(context.Background(), id)
	}
	if err != nil {
		return printErr("Budget channel operation failed", err)
	}
	if asJSON {
		if err := json.NewEncoder(osStdout).Encode(out); err != nil {
			return printErr("Could not print budget channels", err)
		}
		return 0
	}
	if err := printBillingBudgetChannels(out); err != nil {
		return printErr("Could not print budget channels", err)
	}
	return 0
}

func printBillingBudgetChannels(out api.FinancialBudgetChannelsResponse) error {
	if len(out.Channels) == 0 {
		_, err := fmt.Fprintln(osStdout, "No channels: alerts go to the account email only.")
		return err
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tKIND\tTARGET\tLAST DELIVERED\tLAST ERROR"); err != nil {
		return err
	}
	for _, c := range out.Channels {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Name, c.Kind, c.Target, c.LastDeliveredAt, c.LastError); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func runBillingBudget(verb, id, file, key string, expected, after int64, limit int, asJSON bool) int {
	var request api.CreateFinancialBudgetRequest
	if verb == "create" || verb == "update" {
		if file == "" {
			return printErr("Budget file required", errors.New("use --file PATH"))
		}
		spec, err := readFinancialBudgetSpec(file)
		if err != nil {
			return printErr("Invalid budget spec", err)
		}
		request.Spec = spec
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if key != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, key)
	}
	var out any
	switch verb {
	case "list":
		out, err = client.ListFinancialBudgets(ctx)
	case "get":
		out, err = client.GetFinancialBudget(ctx, id)
	case "create":
		out, err = client.CreateFinancialBudget(ctx, request)
	case "update":
		out, err = client.UpdateFinancialBudget(ctx, id, api.UpdateFinancialBudgetRequest{ExpectedRevision: expected, Spec: request.Spec})
	case "delete":
		out, err = client.DeleteFinancialBudget(ctx, id, expected)
	case "history":
		out, err = client.ListFinancialBudgetRevisions(ctx, id, after, limit)
	}
	if err != nil {
		return printErr("Budget operation failed", err)
	}
	if asJSON {
		if err := json.NewEncoder(osStdout).Encode(out); err != nil {
			return printErr("Could not print budget", err)
		}
		return 0
	}
	if err := printBillingBudgetResult(out); err != nil {
		return printErr("Could not print budget", err)
	}
	return 0
}

func printBillingBudgetResult(out any) error {
	switch value := out.(type) {
	case api.FinancialBudgetResponse:
		if _, err := fmt.Fprintf(osStdout, "%s\nID: %s\nRevision: %d\nStatus: %s\nLimit: %s (%s)\n", value.Spec.Name, value.ID, value.Revision, value.Status, financialMoney(value.Spec.LimitMillicents), value.Spec.Basis); err != nil {
			return err
		}
		if value.NotificationReady {
			effect := "Spending notifications are active; workloads continue running."
			if financial.PreviewStopping(value.Spec) {
				effect = "Preview stopping is active; production can continue. Metering, drain and teardown delays can exceed the limit."
			}
			if _, err := fmt.Fprintln(osStdout, effect); err != nil {
				return err
			}
			return printBillingBudgetRuntime(value.Runtime)
		}
		if !value.EnforcementReady {
			_, err := fmt.Fprintln(osStdout, "Enforcement is unavailable; this policy does not protect spending.")
			return err
		}
	case api.FinancialBudgetListResponse:
		tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "ID\tNAME\tREVISION\tSTATUS\tLIMIT"); err != nil {
			return err
		}
		for _, p := range value.Budgets {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", p.ID, p.Spec.Name, p.Revision, p.Status, financialMoney(p.Spec.LimitMillicents)); err != nil {
				return err
			}
		}
		return tw.Flush()
	case api.FinancialBudgetHistoryResponse:
		tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "REVISION\tMUTATION\tACTOR\tRECORDED"); err != nil {
			return err
		}
		for _, p := range value.Revisions {
			if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", p.Revision, p.Mutation, p.Actor, p.RecordedAt.Format("2006-01-02T15:04:05Z07:00")); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if value.NextRevision != 0 {
			_, err := fmt.Fprintf(osStdout, "Continue with --after-revision %d\n", value.NextRevision)
			return err
		}
	}
	return nil
}

func printBillingBudgetRuntime(runtime *financial.BudgetRuntime) error {
	if runtime != nil && runtime.Compute != nil {
		b := runtime.Compute
		if b.JobID != "" {
			if _, err := fmt.Fprintf(osStdout, "Job compute budget: %s\n", b.JobID); err != nil {
				return err
			}
		}
		label := "compute"
		if b.Egress != nil {
			label = "compute and egress"
		}
		if _, err := fmt.Fprintf(osStdout, "Committed %s (conservative): %s\nReserved %s: %s\nRemaining %s: %s\nUnconfirmed VM attempts: %d; budget held: %t\n", label, financialMoney(b.CommittedMillicents), label, financialMoney(b.ReservedMillicents), label, financialMoney(b.RemainingMillicents), b.UnconfirmedLeases, b.Held); err != nil {
			return err
		}
		if b.ScopeKind == "account" {
			if _, err := fmt.Fprintf(osStdout, "Shared account allowance (MB-seconds): granted %d; committed %d; reserved %d; remaining %d\n", b.AllowanceGranted, b.AllowanceCommitted, b.AllowanceReserved, b.AllowanceRemaining); err != nil {
				return err
			}
		}
		if e := b.Egress; e != nil {
			if _, err := fmt.Fprintf(osStdout, "Egress funding: committed %s; reserved %s\nInterface byte credits: committed %d; reserved %d\nShared included egress bytes: granted %d; committed %d; reserved %d; remaining %d\nUnused or uncertain credits commit conservatively after shutdown; invoice usage remains measured bytes.\n", financialMoney(e.CommittedMillicents), financialMoney(e.ReservedMillicents), e.CommittedBytes, e.ReservedBytes, e.AllowanceGranted, e.AllowanceCommitted, e.AllowanceReserved, e.AllowanceRemaining); err != nil {
				return err
			}
		}
		if !b.Initialized {
			if _, err := fmt.Fprintln(osStdout, "Awaiting accounting initialization for the current UTC month."); err != nil {
				return err
			}
		}
		if runtime.Evaluation == nil {
			return nil
		}
	}
	if runtime != nil {
		for _, stop := range runtime.Stops {
			if _, err := fmt.Fprintf(osStdout, "Preview stop %s: requested %s; drain deadline %s; resume %s\n", stop.ID, stop.RequestedAt.Format(time.RFC3339), stop.DrainDeadline.Format(time.RFC3339), stop.ResumeRule); err != nil {
				return err
			}
			if len(stop.Targets) == 0 {
				if _, err := fmt.Fprintln(osStdout, "Awaiting scheduler acknowledgement; stopping is not yet confirmed."); err != nil {
					return err
				}
			}
			for _, target := range stop.Targets {
				if _, err := fmt.Fprintf(osStdout, "  %s %s: %s; resident=%d; observed=%s; %s\n", target.AppID, target.EnvironmentID, target.State, target.ResidentCount, target.ObservedAt.Format(time.RFC3339), target.LastError); err != nil {
					return err
				}
			}
		}
	}
	if runtime == nil || runtime.Evaluation == nil {
		_, err := fmt.Fprintln(osStdout, "Awaiting first evaluation.")
		return err
	}
	e := runtime.Evaluation
	amount := "unavailable"
	if e.KnownMillicents != nil {
		amount = financialMoney(*e.KnownMillicents)
	}
	if _, err := fmt.Fprintf(osStdout, "Known spending: %s\nLast evaluation: %s\nCoverage complete: %t; fresh: %t\n", amount, e.EvaluatedAt.Format(time.RFC3339), e.CoverageComplete, e.Fresh); err != nil {
		return err
	}
	for _, reason := range e.Reasons {
		if _, err := fmt.Fprintf(osStdout, "Coverage: %s\n", reason); err != nil {
			return err
		}
	}
	projection := "unavailable (" + e.ProjectionReason + ")"
	if e.ProjectedMillicents != nil {
		projection = financialMoney(*e.ProjectedMillicents) + " at the current rate (an estimate, not an invoice)"
	}
	if _, err := fmt.Fprintf(osStdout, "Projected month end: %s\n", projection); err != nil {
		return err
	}
	for _, a := range runtime.Alerts {
		if _, err := fmt.Fprintf(osStdout, "Alert %s: %s at %s (%d attempts)\n", a.ID, a.State, financialMoney(a.ThresholdMillicents), a.Attempts); err != nil {
			return err
		}
	}
	if a := runtime.Projection; a != nil {
		if _, err := fmt.Fprintf(osStdout, "Projected-overrun alert %s: %s (%d attempts)\n", a.ID, a.State, a.Attempts); err != nil {
			return err
		}
	}
	return nil
}
