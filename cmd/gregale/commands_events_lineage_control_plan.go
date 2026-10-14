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

func cmdEventsLineageControlPlan(args []string, apply bool, preflight ...bool) int {
	checking := len(preflight) > 0 && preflight[0]
	flags, positional := splitArgsForFlags(args)
	mode := "subscription-lineage-retry-control-plan-export"
	if checking {
		mode = "subscription-lineage-retry-control-plan-preflight"
	} else if apply {
		mode = "subscription-lineage-retry-control-plan-apply"
	}
	fs := newFlagSet(mode, flag.ContinueOnError)
	file := fs.String("file", "", "reviewable control-plan JSON path")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum total duration")
	var action, status, attention, after string
	var limit int
	var reportPath, resumePath string
	if apply {
		fs.StringVar(&reportPath, "report", "", "new private append-only execution report")
		fs.StringVar(&resumePath, "resume-report", "", "previous execution report; requires a new --report")
	}
	if !apply && !checking {
		fs.StringVar(&action, "action", "", "pause, resume or cancel")
		fs.StringVar(&status, "status", "", "pending or paused; omit for both")
		fs.StringVar(&attention, "attention", "any", "any, stalled or retention_risk")
		fs.StringVar(&after, "after", "", "attention-list cursor")
		fs.IntVar(&limit, "limit", api.EventExecutionLineageRetryJobsPageDefault, "maximum jobs in this single-page plan")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *file == "" || *timeout <= 0 {
		PrintUsage(os.Stderr, "usage: gregale events "+mode+" APP SUB --file PATH [--timeout 2m]", "events")
		return 1
	}
	sub, err := uuid.Parse(positional[1])
	if err != nil || sub == uuid.Nil {
		return printErr("Invalid subscription", fmt.Errorf("subscription must be a nonzero UUID"))
	}
	var plan lineageControlPlan
	if apply || checking {
		plan, err = readLineageControlPlan(*file)
		if err == nil && plan.SubscriptionID != sub.String() {
			err = fmt.Errorf("plan subscription does not match supplied target")
		}
	} else {
		option := api.EventExecutionLineageRetryJobAttentionOptions{Attention: attention}
		if option.Attention == "" || option.Validate(status) != nil || (action != "pause" && action != "resume" && action != "cancel") || limit < 1 || limit > api.EventExecutionLineageControlPlanMaxJobs {
			err = fmt.Errorf("select a valid attention filter, active status, action and job limit")
		}
	}
	if err != nil {
		return printErr("Invalid control plan or selection", err)
	}
	var previous map[int]lineageControlPlanResult
	if resumePath != "" {
		if reportPath == "" {
			return printErr("Invalid report options", fmt.Errorf("resume-report requires a new report destination"))
		}
		previous, err = readLineageControlPlanJournal(resumePath, plan)
		if err != nil {
			return printErr("Invalid execution report", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	if checking {
		return preflightLineageControlPlan(ctx, client, positional[0], plan, *file)
	}
	if apply {
		var run lineageControlPlanRun
		run.previous = previous
		if reportPath != "" {
			run.journal, err = newLineageControlPlanJournal(reportPath, plan)
			if err != nil {
				return printErr("Cannot create execution report", err)
			}
			defer func() { _ = run.journal.file.Close() }()
		}
		return applyLineageControlPlan(ctx, client, positional[0], plan, *file, run)
	}
	readCtx, readCancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	page, err := client.ListEventExecutionLineageRetryJobs(readCtx, positional[0], sub.String(), status, after, limit, api.EventExecutionLineageRetryJobAttentionOptions{Attention: attention})
	readCancel()
	if err != nil {
		return lineageControlExportError(ctx, "Attention selection failed", err)
	}
	plan, err = buildLineageControlPlan(page, sub.String(), status, attention, action)
	if err == nil {
		err = writeLineagePrivateArtifact(ctx, *file, plan, api.EventExecutionLineageControlPlanMaxBytes)
	}
	if err != nil {
		return lineageControlExportError(ctx, "Control plan export failed", err)
	}
	summary := struct {
		PlanID    string `json:"plan_id"`
		File      string `json:"file"`
		Selected  int    `json:"selected"`
		NextAfter string `json:"next_after,omitempty"`
	}{plan.PlanID, *file, len(plan.Actions), plan.Source.NextAfter}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %d guarded %s commands to %s | plan %s\n", len(plan.Actions), action, oneLine(*file), plan.PlanID)
	if plan.Source.NextAfter != "" {
		_, _ = fmt.Fprintf(osStdout, "More matching jobs were observed: --after %s\n", oneLine(plan.Source.NextAfter))
	}
	_, _ = fmt.Fprintln(osStdout, "Review this fixed file before control-plan-apply. Repeat the same file after an unconfirmed response.")
	return 0
}
