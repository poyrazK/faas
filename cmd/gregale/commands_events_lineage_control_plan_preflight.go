package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lineageControlPlanPreflightReport struct {
	PlanID         string                     `json:"plan_id"`
	File           string                     `json:"file"`
	AppID          string                     `json:"app_id"`
	SubscriptionID string                     `json:"subscription_id"`
	Advisory       bool                       `json:"advisory"`
	Complete       bool                       `json:"complete"`
	Ready          bool                       `json:"ready"`
	Results        []lineageControlPlanResult `json:"results"`
}

func preflightLineageControlPlan(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, path string) int {
	report := lineageControlPlanPreflightReport{PlanID: plan.PlanID, File: path, AppID: plan.AppID, SubscriptionID: plan.SubscriptionID, Advisory: true, Complete: true, Ready: true, Results: make([]lineageControlPlanResult, 0, len(plan.Actions))}
	scope := lineageRetryRequestFile{Scope: &lineageRetryFileScope{AppID: plan.AppID, SubscriptionID: plan.SubscriptionID}}
	scopeErr := scope.validateApp(ctx, client, app)
	for _, entry := range plan.Actions {
		result := lineageControlPlanResult{JobRequestID: entry.JobRequestID, CommandID: entry.CommandID, Action: entry.Action, Outcome: "unverified"}
		err := scopeErr
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			result, err = preflightLineageControlPlanEntry(ctx, client, app, plan, entry)
		}
		if err != nil {
			result.Error = err.Error()
			report.Complete = false
		}
		if result.Outcome != "ready" && result.Outcome != "no_op" && result.Outcome != "already_acknowledged" {
			report.Ready = false
		}
		report.Results = append(report.Results, result)
	}
	code := 0
	if !report.Ready {
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
	_, _ = fmt.Fprintf(osStdout, "Plan %s | advisory ready %t | complete %t\n", plan.PlanID, report.Ready, report.Complete)
	for _, result := range report.Results {
		_, _ = fmt.Fprintf(osStdout, "Job %s | %s | %s | command %s | %s\n", result.JobRequestID, result.Action, result.Outcome, result.CommandID, oneLine(result.Error))
	}
	_, _ = fmt.Fprintln(osStdout, "Read-only observations do not reserve state, retention or receipt slots, or verify write permission/MFA. Review before applying the same file.")
	return code
}

func preflightLineageControlPlanEntry(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, entry lineageControlPlanEntry) (lineageControlPlanResult, error) {
	result := lineageControlPlanResult{JobRequestID: entry.JobRequestID, CommandID: entry.CommandID, Action: entry.Action, Outcome: "unverified"}
	readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	job, err := client.GetEventExecutionLineageRetryJob(readCtx, app, plan.SubscriptionID, entry.JobRequestID)
	cancel()
	if err != nil {
		return result, err
	}
	if !sameBindingDeployment(job.AppID, plan.AppID) || !sameBindingDeployment(job.SubscriptionID, plan.SubscriptionID) || !sameBindingDeployment(job.RequestID, entry.JobRequestID) {
		return result, fmt.Errorf("job scope does not match plan")
	}
	if !job.CreatedAt.Equal(entry.ExpectedCreatedAt) || !job.RetainUntil.Equal(entry.RetainUntil) {
		result.Outcome = "stale_guard"
		return result, nil
	}
	if _, err = summarizeLineageRetryWatch(job); err != nil {
		return result, err
	}
	result.Status, result.Processed, result.Selected = job.Status, job.Processed, len(job.Items)
	if !job.RetainUntil.After(time.Now().UTC()) {
		result.Outcome = "expired"
		return result, nil
	}
	readCtx, cancel = context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	history, err := client.ListEventExecutionLineageRetryJobControls(readCtx, app, plan.SubscriptionID, entry.JobRequestID, entry.ExpectedCreatedAt, 0, api.EventExecutionLineageRetryControlPageMax)
	cancel()
	if err != nil {
		return result, err
	}
	bundle, err := buildLineageControlHistoryExport(job, history, app, api.EventExecutionLineageRetryJobControlFilters{}, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if !job.RetainUntil.After(time.Now().UTC()) {
		result.Outcome = "expired"
		return result, nil
	}
	for _, receipt := range bundle.Controls {
		if !sameBindingDeployment(receipt.CommandID, entry.CommandID) {
			continue
		}
		if receipt.Action != entry.Action {
			result.Outcome = "command_conflict"
			return result, nil
		}
		result.Outcome = "already_acknowledged"
		result.Receipt = &receipt
		return result, nil
	}
	if len(bundle.Controls) >= api.EventExecutionLineageRetryControlMaxReceipts {
		result.Outcome = "receipt_limit"
		return result, nil
	}
	terminal := job.Status == "completed" || job.Status == "cancelled"
	if terminal && entry.Action != "cancel" {
		result.Outcome = "terminal"
		return result, nil
	}
	result.Outcome = "ready"
	if terminal || entry.Action == "pause" && job.Status == "paused" || entry.Action == "resume" && job.Status == "pending" {
		result.Outcome = "no_op"
	}
	return result, nil
}
