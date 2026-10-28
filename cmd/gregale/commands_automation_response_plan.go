package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"time"
)

type automationResponsePlan = api.WorkflowRecoveryResponsePlan
type automationResponseAction = api.WorkflowRecoveryResponseAction
type automationResponseOutcome struct {
	ResumeCount int    `json:"resume_count"`
	Kind        string `json:"kind"`
	Position    int64  `json:"position"`
	RunID       string `json:"run_id"`
	RequestID   string `json:"request_id"`
	JobID       string `json:"job_id,omitempty"`
	Status      string `json:"status"`
	Durable     bool   `json:"durable"`
	Resolved    bool   `json:"resolved"`
	Error       string `json:"error,omitempty"`
}

func responseActionGeneration(a automationResponseAction) int {
	if a.Cancellation != nil {
		return *a.Cancellation.ExpectedResumeCount
	}
	return a.Retry.WorkflowRuns.Runs[0].ExpectedResumeCount + 1
}
func responseActionID(p automationResponsePlan, a automationResponseAction) string {
	return api.WorkflowRecoveryResponseActionID(p, a)
}
func validateAutomationResponsePlan(p automationResponsePlan) error {
	if p.Version != 1 || p.App == "" || !workflowUUIDPattern.MatchString(p.AppID) || !workflowUUIDPattern.MatchString(p.JobID) || p.CreatedAt.IsZero() || len(p.Actions) < 1 || len(p.Actions) > api.AutomationRecoveryBatchMaxRuns {
		return errors.New("invalid response plan scope or selection")
	}
	if err := validateDeployIdempotencyKey(p.Key); err != nil {
		return err
	}
	seen := map[int64]bool{}
	runs := map[string]bool{}
	for _, a := range p.Actions {
		if a.Position < 1 || a.Position > api.AutomationRecoveryBatchMaxRuns || seen[a.Position] || runs[a.RunID] || !workflowUUIDPattern.MatchString(a.RunID) {
			return errors.New("invalid or duplicate response target")
		}
		seen[a.Position] = true
		runs[a.RunID] = true
		id := responseActionID(p, a)
		switch a.Kind {
		case "cancel":
			if a.Cancellation == nil || a.Retry != nil || a.Cancellation.RequestID != id || a.Cancellation.Position != a.Position {
				return errors.New("invalid cancellation identity")
			}
			if err := a.Cancellation.Validate(); err != nil {
				return err
			}
		case "retry":
			if a.Retry == nil || a.Cancellation != nil || a.Retry.RequestID != id || a.Retry.ParentJobID != p.JobID || a.Retry.Mode != "workflow" || a.Retry.WorkflowRuns == nil || len(a.Retry.WorkflowRuns.Runs) != 1 {
				return errors.New("invalid retry identity")
			}
			target := a.Retry.WorkflowRuns.Runs[0]
			if target.RunID != a.RunID || target.ParentPosition != a.Position || target.ExpectedResumeCount < 1 || a.Retry.RatePerSecond < 1 {
				return errors.New("invalid retry target")
			}
			if err := a.Retry.Validate(); err != nil {
				return err
			}
		default:
			return errors.New("unknown response action")
		}
	}
	return nil
}
func saveAutomationResponsePlan(path string, p automationResponsePlan) error {
	if err := validateAutomationResponsePlan(p); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := persistRecoveryRecord(f, p); err != nil {
		return err
	}
	return f.Close()
}
func readAutomationResponsePlan(path string) (automationResponsePlan, error) {
	var p automationResponsePlan
	f, err := openCustomerFile(path)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.AutomationRecoveryPlanMaxBytes {
		return p, errors.New("response plan must be a regular file of at most 64 KiB")
	}
	d := json.NewDecoder(io.LimitReader(f, api.AutomationRecoveryPlanMaxBytes+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return p, errors.New("expected one response plan object")
	}
	return p, validateAutomationResponsePlan(p)
}
func cmdAutomationsResponsePlan(args []string) int {
	fs := newFlagSet("automations recovery-response-plan", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	job := fs.String("job", "", "native recovery job")
	key := fs.String("key", "", "stable response key")
	path := fs.String("plan-out", "", "new plan file")
	reason := fs.String("reason", "", "operator reason")
	rate := fs.Int("rate", api.EventRecoveryRateDefault, "retry admission rate")
	var cancels, retries multiFlag
	fs.Var(&cancels, "cancel-position", "item to cancel (repeatable)")
	fs.Var(&retries, "retry-position", "saved failed item to retry (repeatable)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || !workflowUUIDPattern.MatchString(*job) || *path == "" || len(cancels)+len(retries) == 0 || len(cancels)+len(retries) > api.AutomationRecoveryBatchMaxRuns {
		return printErr("Invalid response selection", errors.New("provide --app, --job, --key, --reason, --plan-out and explicit action positions"))
	}
	if err := validateDeployIdempotencyKey(*key); err != nil {
		return printErr("Invalid response key", err)
	}
	count := 1
	if err := (api.WorkflowRecoveryCancellationRequest{Position: 1, ExpectedResumeCount: &count, Reason: *reason}).Validate(); err != nil {
		return printErr("Invalid response reason", err)
	}
	if *rate < 1 || *rate > api.EventRecoveryRateMax {
		return printErr("Invalid retry rate", errors.New("rate out of range"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	owner, err := c.GetApp(ctx, *app)
	if err != nil {
		return printErr("Could not inspect app", err)
	}
	parent, err := c.GetEventRecovery(ctx, *job)
	if err != nil {
		return printErr("Could not inspect recovery", err)
	}
	if parent.AppID != owner.ID || parent.Selection.WorkflowRuns == nil {
		return printErr("Invalid recovery scope", errors.New("native recovery must belong to the selected app"))
	}
	p := automationResponsePlan{Version: 1, App: *app, AppID: owner.ID, JobID: *job, Key: *key, CreatedAt: time.Now().UTC(), Actions: []automationResponseAction{}}
	for _, raw := range cancels {
		pos, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || pos < 1 || pos > api.AutomationRecoveryBatchMaxRuns {
			return printErr("Invalid cancel position", errors.New("position out of range"))
		}
		items, e := c.ListEventRecoveryItems(ctx, *job, pos-1, 1)
		if e != nil {
			return printErr("Could not inspect cancellation target", e)
		}
		if len(items.Items) != 1 || items.Items[0].Position != pos || items.Items[0].ExpectedResumeCount == nil {
			return printErr("Invalid cancellation target", errors.New("reviewed item is not retained"))
		}
		item := items.Items[0]
		n := *item.ExpectedResumeCount + 1
		a := automationResponseAction{Kind: "cancel", Position: pos, RunID: item.WorkflowRunID, Cancellation: &api.WorkflowRecoveryCancellationRequest{Position: pos, ExpectedResumeCount: &n, Reason: *reason}}
		a.Cancellation.RequestID = responseActionID(p, a)
		out, e := c.PreviewWorkflowRecoveryCancellation(ctx, *job, *a.Cancellation)
		if e != nil {
			return printErr("Cancellation preview failed", e)
		}
		if out.Outcome != "eligible" || out.RunID != a.RunID || out.ResumeCount != n {
			return printErr("Cancellation is not eligible", fmt.Errorf("item %d: %s", pos, out.Outcome))
		}
		p.Actions = append(p.Actions, a)
	}
	if len(retries) > 0 {
		review, e := loadAutomationRecoveryRetryPreview(ctx, c, *app, *job)
		if e != nil {
			return printErr("Could not review retries", e)
		}
		for _, raw := range retries {
			rp, e := automationRecoveryRetryPlan(review, []string{raw}, *key)
			if e != nil {
				return printErr("Retry is not eligible", e)
			}
			target := rp.Runs[0]
			req := automationRunJobRequest(rp, *rate, *reason)
			a := automationResponseAction{Kind: "retry", Position: target.ParentPosition, RunID: target.ID, Retry: &req}
			req.RequestID = responseActionID(p, a)
			p.Actions = append(p.Actions, a)
		}
	}
	if err = saveAutomationResponsePlan(*path, p); err != nil {
		return printErr("Could not save response plan", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(p))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %d reviewed actions: %s\nPreview: gregale automation recovery-response-preview --plan %s\n", len(p.Actions), oneLine(*path), automationCommandArgument(*path))
	return 0
}
func responseNotFound(err error) bool {
	var p *api.APIError
	return errors.As(err, &p) && p.Problem.Status == http.StatusNotFound
}
func responseRetryMatches(job api.EventRecoveryJob, p automationResponsePlan, a automationResponseAction) bool {
	req := *a.Retry
	req.Reason = ""
	return job.ID != "" && job.AppID == p.AppID && reflect.DeepEqual(job.Selection, req)
}
func inspectAutomationResponse(ctx context.Context, c *api.Client, p automationResponsePlan, a automationResponseAction, mode string) (automationResponseOutcome, error) {
	out := automationResponseOutcome{Kind: a.Kind, Position: a.Position, RunID: a.RunID, RequestID: responseActionID(p, a), Status: "unresolved_no_receipt"}
	out.ResumeCount = responseActionGeneration(a)
	if a.Kind == "cancel" {
		receipt, err := c.GetWorkflowRecoveryActionReceipt(ctx, p.JobID, out.RequestID)
		if err == nil {
			if !reflect.DeepEqual(receipt.Request, *a.Cancellation) || receipt.Response.JobID != p.JobID || receipt.Response.Position != a.Position || receipt.Response.ResumeCount != *a.Cancellation.ExpectedResumeCount || receipt.Response.Preview || receipt.CapturedAt.IsZero() {
				return out, errors.New("cancellation receipt identity does not match plan")
			}
			if receipt.Response.RunID != "" && receipt.Response.RunID != a.RunID {
				return out, errors.New("cancellation receipt run does not match plan")
			}
			out.Status = receipt.Response.Outcome
			out.Durable = true
			out.Resolved = out.Status == "cancelled" || out.Status == "already_cancelled"
			return out, nil
		}
		if !responseNotFound(err) {
			return out, err
		}
		if mode == "status" {
			return out, nil
		}

		parent, e := c.GetEventRecovery(ctx, p.JobID)
		if e != nil {
			return out, e
		}
		if parent.AppID != p.AppID || parent.Selection.WorkflowRuns == nil {
			return out, errors.New("cancellation parent scope does not match plan")
		}
		items, e := c.ListEventRecoveryItems(ctx, p.JobID, a.Position-1, 1)
		if e != nil {
			return out, e
		}
		if items.JobID != p.JobID || len(items.Items) != 1 || items.Items[0].Position != a.Position || items.Items[0].WorkflowRunID != a.RunID || items.Items[0].ExpectedResumeCount == nil || *items.Items[0].ExpectedResumeCount+1 != *a.Cancellation.ExpectedResumeCount {
			return out, errors.New("cancellation target does not match saved plan")
		}
		var result api.WorkflowRecoveryCancellationResponse
		if mode == "apply" {
			result, err = c.CancelWorkflowRecoveryExecution(ctx, p.JobID, *a.Cancellation)
		} else {
			result, err = c.PreviewWorkflowRecoveryCancellation(ctx, p.JobID, *a.Cancellation)
		}
		if err != nil {
			return out, err
		}
		if result.JobID != p.JobID || result.Position != a.Position || result.RunID != "" && result.RunID != a.RunID || result.ResumeCount != *a.Cancellation.ExpectedResumeCount {
			return out, errors.New("cancellation response identity does not match plan")
		}
		out.Status = result.Outcome
		out.Durable = mode == "apply"
		out.Resolved = out.Status == "cancelled" || out.Status == "already_cancelled" || mode == "preview" && out.Status == "eligible"
		return out, nil
	}
	job, err := c.GetEventRecoveryByRequest(ctx, p.App, out.RequestID)
	if responseNotFound(err) {
		if mode == "status" {
			return out, nil
		}
		if mode == "preview" {
			review, e := loadAutomationRecoveryRetryPreview(ctx, c, p.App, p.JobID)
			if e != nil {
				return out, e
			}
			if review.Name != a.Retry.WorkflowRuns.Name || review.AppID != p.AppID {
				return out, errors.New("retry preview scope does not match plan")
			}
			target := a.Retry.WorkflowRuns.Runs[0]
			out.Status = "changed"
			for _, row := range review.Rows {
				if row.Position == a.Position && row.RunID == a.RunID && row.ExpectedResumeCount == target.ExpectedResumeCount {
					out.Status = row.Reason
					out.Resolved = row.Selectable
					break
				}
			}
			return out, nil
		}
		job, err = c.CreateEventRecovery(ctx, p.App, *a.Retry)
	}
	if err != nil {
		return out, err
	}
	if !responseRetryMatches(job, p, a) {
		return out, errors.New("retry receipt selection does not match plan")
	}
	out.Durable = true
	out.JobID = job.ID
	out.Status = "admission_pending"
	items, err := c.ListEventRecoveryItems(ctx, job.ID, 0, 1)
	if err != nil {
		return out, err
	}
	if items.JobID != job.ID || len(items.Items) != 1 {
		return out, errors.New("retry job item is not retained")
	}
	item := items.Items[0]
	target := a.Retry.WorkflowRuns.Runs[0]
	if item.WorkflowRunID != a.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount != target.ExpectedResumeCount || item.ParentJobID != p.JobID || item.ParentPosition == nil || *item.ParentPosition != a.Position {
		return out, errors.New("retry job target does not match plan")
	}
	switch item.State {
	case "queued":
		out.Status = "execution_unknown"
		if item.Execution != nil {
			out.Status = "execution_" + item.Execution.State
			if item.Execution.Source == "recovery_result" && item.Execution.EvidenceSource == "workflow_run" && item.Execution.RecordedAt != nil && (item.Execution.State == "succeeded" || item.Execution.State == "failed" || item.Execution.State == "cancelled") {
				out.Resolved = true
			}
		}
	case "skipped", "cancelled":
		out.Status = "admission_" + item.State
	}
	return out, nil
}
func cmdAutomationsResponseExecute(args []string, mode string) int {
	fs := newFlagSet("automations recovery-response-"+mode, flag.ContinueOnError)
	path := fs.String("plan", "", "saved response plan")
	yes := fs.Bool("yes", false, "apply reviewed actions")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *path == "" || mode == "apply" && !*yes {
		return printErr("Response plan confirmation required", errors.New("provide --plan; applying requires --yes"))
	}
	p, err := readAutomationResponsePlan(*path)
	if err != nil {
		return printErr("Invalid response plan", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := c.GetApp(ctx, p.App)
	if err != nil {
		return printErr("Could not inspect app", err)
	}
	if app.ID != p.AppID {
		return printErr("Response scope changed", errors.New("app identity changed"))
	}
	report := struct {
		JobID      string                      `json:"job_id"`
		Mode       string                      `json:"mode"`
		Unresolved int                         `json:"unresolved"`
		Actions    []automationResponseOutcome `json:"actions"`
	}{JobID: p.JobID, Mode: mode, Actions: []automationResponseOutcome{}}
	for _, a := range p.Actions {
		out, e := inspectAutomationResponse(ctx, c, p, a, mode)
		if e != nil {
			out.Status = "unknown"
			out.Resolved = false
			out.Error = e.Error()
		}
		if !out.Resolved {
			report.Unresolved++
		}
		report.Actions = append(report.Actions, out)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		for _, out := range report.Actions {
			_, _ = fmt.Fprintf(osStdout, "%s | item %d | run %s | generation %d | %s | durable %t | request %s\n", out.Kind, out.Position, oneLine(out.RunID), out.ResumeCount, oneLine(out.Status), out.Durable, out.RequestID)
			if out.Error != "" {
				_, _ = fmt.Fprintf(osStdout, "  %s\n", oneLine(out.Error))
			}
		}
		_, _ = fmt.Fprintf(osStdout, "Unresolved: %d. Reconcile with recovery-response-status --plan %s.\n", report.Unresolved, automationCommandArgument(*path))
	}
	if report.Unresolved > 0 {
		return 5
	}
	return 0
}
