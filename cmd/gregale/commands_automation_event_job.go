package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func automationEventJobRequest(p automationEventPlan, rate int, reason string) api.EventRecoveryRequest {
	targets := make([]api.EventWorkflowRecoveryTarget, 0, len(p.Events))
	for _, item := range p.Events {
		targets = append(targets, api.EventWorkflowRecoveryTarget{EventID: item.ID, SubscriptionID: item.Subscription, ReplayCount: item.ReplayCount, Generation: item.Generation})
	}
	identity := fmt.Sprintf("automation-event-job:%q:%q:%q", p.AppID, p.Name, p.Key)
	return api.EventRecoveryRequest{RequestID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String(), Mode: "routing", ProtectReceipts: true, RatePerSecond: rate, Reason: reason, WorkflowRouting: &api.EventWorkflowRecoveryPlan{Name: p.Name, PublishedVersion: p.Revision, Source: p.Source, Events: targets}}
}

func cmdAutomationsEventRecoveryStart(args []string) int {
	fs := newFlagSet("automations event-recovery-start", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed event plan")
	rate := fs.Int("rate", api.EventRecoveryRateDefault, "maximum routing recoveries per second")
	reason := fs.String("reason", "", "optional operator reason")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *path == "" || *rate < 1 || *rate > api.EventRecoveryRateMax {
		return printErr("Invalid arguments", errors.New("provide a plan and a valid rate"))
	}
	p, err := readAutomationEventPlan(*path)
	if err != nil {
		return printErr("Invalid event recovery plan", err)
	}
	req := automationEventJobRequest(p, *rate, *reason)
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
		return printErr("Event plan scope changed", errors.New("app identity changed"))
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
	_, _ = fmt.Fprintf(osStdout, "Inspect: gregale event recovery get %s\nItems: gregale event recovery items %s\n", oneLine(job.ID), oneLine(job.ID))
	return 0
}
