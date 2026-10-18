package main

import (
	"context"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

type lineageControlPlanResult struct {
	Evidence     string                                           `json:"evidence,omitempty"`
	JobRequestID string                                           `json:"job_request_id"`
	CommandID    string                                           `json:"command_id"`
	Action       string                                           `json:"action"`
	Outcome      string                                           `json:"outcome"`
	Error        string                                           `json:"error,omitempty"`
	Status       string                                           `json:"status,omitempty"`
	Processed    int                                              `json:"processed,omitempty"`
	Selected     int                                              `json:"selected,omitempty"`
	Receipt      *api.EventExecutionLineageRetryJobControlReceipt `json:"receipt,omitempty"`
}
type lineageControlPlanReport struct {
	ExecutionReport  string                     `json:"execution_report,omitempty"`
	PersistenceError string                     `json:"persistence_error,omitempty"`
	PlanID           string                     `json:"plan_id"`
	File             string                     `json:"file"`
	AppID            string                     `json:"app_id"`
	SubscriptionID   string                     `json:"subscription_id"`
	Complete         bool                       `json:"complete"`
	Acknowledged     int                        `json:"acknowledged"`
	Unconfirmed      int                        `json:"unconfirmed"`
	NotAttempted     int                        `json:"not_attempted"`
	Results          []lineageControlPlanResult `json:"results"`
}

func applyLineageControlPlan(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, path string, run lineageControlPlanRun) int {
	report := lineageControlPlanReport{PlanID: plan.PlanID, File: path, AppID: plan.AppID, SubscriptionID: plan.SubscriptionID, Results: make([]lineageControlPlanResult, 0, len(plan.Actions))}
	if run.journal != nil {
		report.ExecutionReport = run.journal.file.Name()
	}
	scope := lineageRetryRequestFile{Scope: &lineageRetryFileScope{AppID: plan.AppID, SubscriptionID: plan.SubscriptionID}}
	scopeErr := scope.validateApp(ctx, client, app)
	var persistenceErr error
	for index, entry := range plan.Actions {
		result := lineageControlPlanResult{JobRequestID: entry.JobRequestID, CommandID: entry.CommandID, Action: entry.Action, Outcome: "not_attempted"}
		if scopeErr != nil {
			result.Error = scopeErr.Error()
		} else if persistenceErr != nil {
			result.Error = "execution report persistence failed; no further commands attempted"
		} else if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
		} else {
			result, persistenceErr = runLineageControlPlanEntry(ctx, client, app, plan, entry, index, run)
		}
		if run.journal != nil && persistenceErr == nil {
			persistenceErr = run.journal.append(lineageControlPlanJournalRecord{Index: index, Result: result})
		}
		switch result.Outcome {
		case "acknowledged":
			report.Acknowledged++
		case "unconfirmed":
			report.Unconfirmed++
		default:
			report.NotAttempted++
		}
		report.Results = append(report.Results, result)
	}
	if persistenceErr != nil {
		report.PersistenceError = persistenceErr.Error()
		_, _ = fmt.Fprintf(os.Stderr, "Execution report persistence failed: %s; resume using the same plan and last complete report records.\n", oneLine(persistenceErr.Error()))
	}
	report.Complete = report.NotAttempted == 0
	code := 0
	if report.Unconfirmed > 0 || report.NotAttempted > 0 || persistenceErr != nil {
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
	_, _ = fmt.Fprintf(osStdout, "Plan %s | acknowledged %d | unconfirmed %d | not attempted %d\n", plan.PlanID, report.Acknowledged, report.Unconfirmed, report.NotAttempted)
	for _, result := range report.Results {
		_, _ = fmt.Fprintf(osStdout, "Job %s | %s | %s | command %s | %s\n", result.JobRequestID, result.Action, result.Outcome, result.CommandID, oneLine(result.Error))
	}
	return code
}
func executeLineageControlPlanEntry(ctx context.Context, client *api.Client, app, sub string, entry lineageControlPlanEntry) (api.EventExecutionLineageRetryJob, error) {
	req := api.ControlEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: entry.ExpectedCreatedAt, CommandID: entry.CommandID}
	switch entry.Action {
	case "pause":
		return client.PauseEventExecutionLineageRetryJob(ctx, app, sub, entry.JobRequestID, req)
	case "resume":
		return client.ResumeEventExecutionLineageRetryJob(ctx, app, sub, entry.JobRequestID, req)
	case "cancel":
		return client.CancelEventExecutionLineageRetryJob(ctx, app, sub, entry.JobRequestID, api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: entry.ExpectedCreatedAt, CommandID: entry.CommandID})
	}
	return api.EventExecutionLineageRetryJob{}, fmt.Errorf("unsupported plan action")
}
func verifyLineageControlPlanResponse(job api.EventExecutionLineageRetryJob, plan lineageControlPlan, entry lineageControlPlanEntry) error {
	receipt := job.ControlReceipt
	if !sameBindingDeployment(job.AppID, plan.AppID) || !sameBindingDeployment(job.SubscriptionID, plan.SubscriptionID) || !sameBindingDeployment(job.RequestID, entry.JobRequestID) || !job.CreatedAt.Equal(entry.ExpectedCreatedAt) || !job.RetainUntil.Equal(entry.RetainUntil) || receipt == nil || receipt.Sequence < 1 || receipt.Sequence > api.EventExecutionLineageRetryControlMaxReceipts || !sameBindingDeployment(receipt.CommandID, entry.CommandID) || receipt.Action != entry.Action || !receipt.ExpectedCreatedAt.Equal(entry.ExpectedCreatedAt) {
		return fmt.Errorf("control response lacks the original guarded command acknowledgment; repeat the same plan")
	}
	if _, err := summarizeLineageRetryWatch(job); err != nil {
		return err
	}
	return validateLineageControlExportReceipt(*receipt, job, receipt.Sequence)
}

// Persist uncertainty before a POST: a crash can never turn an attempted command
// into a durable assertion that it was not sent.
func runLineageControlPlanEntry(ctx context.Context, client *api.Client, app string, plan lineageControlPlan, entry lineageControlPlanEntry, index int, run lineageControlPlanRun) (lineageControlPlanResult, error) {
	result := lineageControlPlanResult{JobRequestID: entry.JobRequestID, CommandID: entry.CommandID, Action: entry.Action, Outcome: "unconfirmed"}
	if _, seen := run.previous[index]; seen {
		observed, err := preflightLineageControlPlanEntry(ctx, client, app, plan, entry)
		if err != nil {
			result.Error = "resume receipt inspection failed: " + err.Error()
			return result, nil //nolint:nilerr // Independent command failures are recorded in the result; returned errors stop the batch only for journal persistence failures.
		}
		if observed.Outcome == "already_acknowledged" {
			observed.Outcome = "acknowledged"
			return observed, nil
		}
		if observed.Outcome != "ready" && observed.Outcome != "no_op" {
			result.Error = "resume inspection blocked command: " + observed.Outcome
			return result, nil
		}
	}
	if run.journal != nil {
		intent := result
		intent.Error = "command may be submitted; final acknowledgment not yet persisted"
		if err := run.journal.append(lineageControlPlanJournalRecord{Index: index, Result: intent}); err != nil {
			result.Outcome = "not_attempted"
			result.Error = "could not persist command intent"
			return result, err
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	job, err := executeLineageControlPlanEntry(requestCtx, client, app, plan.SubscriptionID, entry)
	cancel()
	if err == nil {
		err = verifyLineageControlPlanResponse(job, plan, entry)
	}
	if err != nil {
		result.Error = err.Error()
		return result, nil //nolint:nilerr // Preserve the unconfirmed command result and continue independent entries; no acknowledgment is inferred.
	}
	result.Outcome = "acknowledged"
	result.Status, result.Processed, result.Selected, result.Receipt = job.Status, job.Processed, len(job.Items), job.ControlReceipt
	return result, nil
}
