package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageControlPlanReconcile(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-control-plan-reconcile", flag.ContinueOnError)
	file := fs.String("file", "", "unchanged reviewed control plan")
	source := fs.String("source-report", "", "previous execution report")
	destination := fs.String("report", "", "new private reconciled execution report")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum total duration")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *file == "" || *source == "" || *destination == "" || *timeout <= 0 {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-control-plan-reconcile APP SUB --file PLAN --source-report PREVIOUS --report NEW [--timeout 2m]", "events")
		return 1
	}
	sub, err := uuid.Parse(positional[1])
	if err != nil || sub == uuid.Nil {
		return printErr("Invalid subscription", fmt.Errorf("subscription must be a nonzero UUID"))
	}
	plan, err := readLineageControlPlan(*file)
	if err != nil {
		return printErr("Invalid control plan", err)
	}
	if plan.SubscriptionID != sub.String() {
		return printErr("Invalid control plan", fmt.Errorf("plan subscription does not match supplied target"))
	}
	previous, err := readLineageControlPlanJournal(*source, plan)
	if err != nil {
		return printErr("Invalid source execution report", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	journal, err := newLineageControlPlanJournal(*destination, plan)
	if err != nil {
		return printErr("Cannot create reconciled report", err)
	}
	defer func() { _ = journal.file.Close() }()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	return reconcileLineageControlPlan(ctx, client, positional[0], plan, *file, *source, journal, previous)
}

type lineageControlPlanReconciliationReport struct {
	PlanID           string                     `json:"plan_id"`
	File             string                     `json:"file"`
	AppID            string                     `json:"app_id"`
	SubscriptionID   string                     `json:"subscription_id"`
	SourceReport     string                     `json:"source_report"`
	ExecutionReport  string                     `json:"execution_report"`
	ReadOnly         bool                       `json:"read_only"`
	Complete         bool                       `json:"complete"`
	RecordedEntries  int                        `json:"recorded_entries"`
	Acknowledged     int                        `json:"acknowledged"`
	Unresolved       int                        `json:"unresolved"`
	PersistenceError string                     `json:"persistence_error,omitempty"`
	Results          []lineageControlPlanResult `json:"results"`
}

func reconcileLineageControlPlan(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, path, source string, journal *lineageControlPlanJournal, previous map[int]lineageControlPlanResult) int {
	report := lineageControlPlanReconciliationReport{PlanID: plan.PlanID, File: path, AppID: plan.AppID, SubscriptionID: plan.SubscriptionID, SourceReport: source, ExecutionReport: journal.file.Name(), ReadOnly: true, Complete: true, RecordedEntries: len(previous), Results: make([]lineageControlPlanResult, 0, len(plan.Actions))}
	scope := lineageRetryRequestFile{Scope: &lineageRetryFileScope{AppID: plan.AppID, SubscriptionID: plan.SubscriptionID}}
	scopeErr := scope.validateApp(ctx, client, app)
	var persistenceErr error
	for index, entry := range plan.Actions {
		result := lineageControlPlanResult{JobRequestID: entry.JobRequestID, CommandID: entry.CommandID, Action: entry.Action, Outcome: "unconfirmed", Evidence: "unavailable"}
		err := scopeErr
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && persistenceErr != nil {
			err = fmt.Errorf("report persistence failed; remaining entries not inspected")
		}
		if err == nil {
			result, err = reconcileLineageControlPlanEntry(ctx, client, app, plan, entry)
		}
		if err != nil {
			result.Error = err.Error()
			report.Complete = false
		}
		if persistenceErr == nil {
			persistenceErr = journal.append(lineageControlPlanJournalRecord{Index: index, Result: result})
		}
		if result.Outcome == "acknowledged" {
			report.Acknowledged++
		} else {
			report.Unresolved++
		}
		report.Results = append(report.Results, result)
	}
	code := 0
	if persistenceErr != nil {
		report.PersistenceError = persistenceErr.Error()
		report.Complete = false
	}
	if report.Unresolved > 0 || persistenceErr != nil {
		code = 1
	}
	if ctx.Err() == context.Canceled {
		code = 130
	}
	if jsonOutput {
		if output := jsonOut(writeJSON(report)); output != 0 {
			return output
		}
		return code
	}
	_, _ = fmt.Fprintf(osStdout, "Plan %s | acknowledged %d | unresolved %d | complete %t | report %s\n", plan.PlanID, report.Acknowledged, report.Unresolved, report.Complete, oneLine(report.ExecutionReport))
	for _, result := range report.Results {
		_, _ = fmt.Fprintf(osStdout, "Job %s | %s | %s | %s | command %s | %s\n", result.JobRequestID, result.Action, result.Outcome, result.Evidence, result.CommandID, oneLine(result.Error))
	}
	if report.PersistenceError != "" {
		_, _ = fmt.Fprintf(os.Stderr, "Execution report persistence failed: %s\n", oneLine(report.PersistenceError))
	}
	_, _ = fmt.Fprintln(osStdout, "No controls sent. Missing receipts are unresolved observations, not proof that commands never ran.")
	return code
}

func reconcileLineageControlPlanEntry(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, entry lineageControlPlanEntry) (lineageControlPlanResult, error) {
	result, err := preflightLineageControlPlanEntry(ctx, client, app, plan, entry)
	observation := result.Outcome
	result.Outcome = "unconfirmed"
	result.Evidence = "unavailable"
	if err != nil {
		return result, err
	}
	switch observation {
	case "already_acknowledged":
		result.Outcome = "acknowledged"
		result.Evidence = "receipt_present"
	case "command_conflict":
		result.Evidence = "command_conflict"
	case "ready", "no_op", "terminal", "receipt_limit":
		result.Evidence = "receipt_absent"
	case "expired":
		result.Evidence = "expired"
	case "stale_guard":
		result.Evidence = "stale_guard"
	default:
		return result, fmt.Errorf("unsupported receipt observation %q", observation)
	}
	return result, nil
}
