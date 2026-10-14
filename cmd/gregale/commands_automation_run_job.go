package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func automationRunJobRequest(p automationRecoveryPlan, rate int, reason string) api.EventRecoveryRequest {
	targets := make([]api.WorkflowRunRecoveryTarget, 0, len(p.Runs))
	for _, item := range p.Runs {
		targets = append(targets, api.WorkflowRunRecoveryTarget{RunID: item.ID, FailureEvidenceID: item.FailureEvidenceID, ExpectedResumeCount: item.ExpectedResumeCount, ParentPosition: item.ParentPosition})
	}
	identity := fmt.Sprintf("automation-run-job:%q:%q:%q", p.AppID, p.Name, p.Key)
	return api.EventRecoveryRequest{ParentJobID: p.ParentJobID, RequestID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String(), Mode: "workflow", RatePerSecond: rate, Reason: reason, WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: p.Name, Runs: targets}}
}

func cmdAutomationsRecoveryStart(args []string) int {
	fs := newFlagSet("automations recovery-start", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed run plan")
	rate := fs.Int("rate", api.EventRecoveryRateDefault, "maximum run resumes per second")
	deadline := fs.Duration("execution-deadline", 0, "advisory execution budget from each admission (zero disables)")
	stall := fs.Duration("execution-stall", 0, "no-progress warning threshold (zero uses 15m)")
	reason := fs.String("reason", "", "optional operator reason")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *path == "" || *rate < 1 || *rate > api.EventRecoveryRateMax {
		return printErr("Invalid arguments", errors.New("provide a plan and a valid rate"))
	}
	p, err := readAutomationRecoveryPlan(*path)
	if err != nil {
		return printErr("Invalid run recovery plan", err)
	}
	if *deadline%time.Second != 0 || *stall%time.Second != 0 {
		return printErr("Invalid execution monitoring duration", errors.New("durations must use whole seconds"))
	}
	req := automationRunJobRequest(p, *rate, *reason)
	req.WorkflowRuns.ExecutionDeadlineSeconds = int(*deadline / time.Second)
	req.WorkflowRuns.ExecutionStallSeconds = int(*stall / time.Second)
	if err := req.Validate(); err != nil {
		return printErr("Invalid job selection", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	app, err := c.GetApp(ctx, p.App)
	if err != nil {
		return printErr("Could not resolve plan scope", err)
	}
	if app.ID != p.AppID {
		return printErr("Run plan scope changed", errors.New("app identity changed"))
	}
	job, err := c.CreateEventRecovery(ctx, p.App, req)
	if err != nil {
		return printErr("Recovery creation failed; retry the original plan", err)
	}
	if job.AppID != p.AppID || job.Selection.RequestID != req.RequestID {
		return printErr("Unexpected recovery response", errors.New("job identity does not match plan"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	writeEventRecoveryJob(job)
	_, _ = fmt.Fprintf(osStdout, "Inspect: gregale events recovery-status %s\nItems: gregale events recovery-items %s\n", oneLine(job.ID), oneLine(job.ID))
	return 0
}
