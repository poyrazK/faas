package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationRunSummary struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	DeploymentID string  `json:"deployment_id,omitempty"`
	CurrentStep  *string `json:"current_step,omitempty"`
	ResumeCount  int     `json:"resume_count"`
	CreatedAt    string  `json:"created_at"`
	ScheduledFor string  `json:"scheduled_for"`
	StartedAt    *string `json:"started_at,omitempty"`
	FinishedAt   *string `json:"finished_at,omitempty"`
	CancelledAt  *string `json:"cancelled_at,omitempty"`
}

func validAutomationRunSelection(app, name string) bool {
	return strings.TrimSpace(app) != "" && name != "" && strings.TrimSpace(name) == name && len(name) <= api.AutomationNameMaxBytes && !strings.ContainsAny(name, "/\\\x00")
}

func loadAutomationRunScope(ctx context.Context, client *api.Client, app, name string) (string, error) {
	application, err := client.GetApp(ctx, app)
	if err != nil {
		return "", err
	}
	if application.ID == "" {
		return "", errors.New("app lookup returned no identity")
	}
	automation, err := client.GetAutomation(ctx, app, name)
	if err != nil {
		return "", err
	}
	if automation.Name != name {
		return "", errors.New("automation lookup returned a different name")
	}
	return application.ID, nil
}

func cmdAutomationsRuns(args []string) int {
	fs := newFlagSet("automations runs", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	status := fs.String("status", "", "run status filter")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "page offset")
	after := fs.String("created-after", "", "inclusive RFC3339 creation-time start")
	before := fs.String("created-before", "", "inclusive RFC3339 creation-time end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validAutomationRunSelection(*app, *name) || *limit < 1 || *limit > 100 || *offset < 0 || !api.ValidWorkflowRunStatus(*status) {
		return printErr("Invalid automation run filters", errors.New("provide --app and --name, a supported --status, --limit 1..100 and a nonnegative --offset"))
	}
	start, err := parseWorkflowRunListTime(*after, "created-after")
	if err != nil {
		return printErr("Invalid automation run time filter", err)
	}
	end, err := parseWorkflowRunListTime(*before, "created-before")
	if err != nil {
		return printErr("Invalid automation run time filter", err)
	}
	if start != nil && end != nil && start.After(*end) {
		return printErr("Invalid automation run time range", errors.New("--created-after must not be later than --created-before"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadAutomationRunScope(ctx, client, *app, *name)
	if err != nil {
		return printErr("Could not resolve automation", err)
	}
	result, err := client.ListWorkflowRunsWithOptions(ctx, *app, api.WorkflowRunListOptions{WorkflowName: *name, Status: *status, Limit: *limit, Offset: *offset, CreatedAfter: start, CreatedBefore: end})
	if err != nil {
		return printErr("Could not list automation runs", err)
	}
	summaries := make([]automationRunSummary, 0, len(result.Runs))
	for _, run := range result.Runs {
		if run.AppID != appID || run.WorkflowName != *name {
			return printErr("Automation run scope mismatch", errors.New("run history returned a run outside the selected app and automation"))
		}
		summaries = append(summaries, automationRunSummary{ID: run.ID, Status: run.Status, DeploymentID: run.DeploymentID, CurrentStep: run.CurrentStep, ResumeCount: run.ResumeCount, CreatedAt: run.CreatedAt, ScheduledFor: run.ScheduledFor, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, CancelledAt: run.CancelledAt})
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App    string                 `json:"app"`
			Name   string                 `json:"name"`
			Total  int                    `json:"total"`
			Limit  int                    `json:"limit"`
			Offset int                    `json:"offset"`
			Runs   []automationRunSummary `json:"runs"`
		}{*app, *name, result.Total, *limit, *offset, summaries}))
	}
	if _, err := fmt.Fprintf(osStdout, "Automation %q in app %q: %d matching runs (offset %d, limit %d).\n", *name, *app, result.Total, *offset, *limit); err != nil {
		return printErr("Could not write automation run history", err)
	}
	renderWorkflowRunsTable(osStdout, result.Runs)
	return 0
}

func cmdAutomationsDiagnose(args []string) int {
	fs := newFlagSet("automations diagnose", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	runID := fs.String("run", "", "workflow run UUID")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validAutomationRunSelection(*app, *name) || !workflowUUIDPattern.MatchString(strings.ToLower(*runID)) {
		return printErr("Invalid automation run selection", errors.New("provide --app, --name and --run with a valid UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadAutomationRunScope(ctx, client, *app, *name)
	if err != nil {
		return printErr("Could not resolve automation", err)
	}
	run, err := client.GetWorkflowRun(ctx, *runID)
	if err != nil {
		return printErr("Could not inspect automation run", err)
	}
	if !strings.EqualFold(run.ID, *runID) || run.AppID != appID || run.WorkflowName != *name {
		return printErr("Automation run scope mismatch", errors.New("the run does not belong to the selected app and automation"))
	}
	diagnostics, err := client.GetWorkflowRunDiagnostics(ctx, *runID)
	if err != nil {
		return printErr("Could not diagnose automation run", err)
	}
	if !strings.EqualFold(diagnostics.RunID, *runID) || diagnostics.WorkflowName != *name {
		return printErr("Automation diagnostics scope mismatch", errors.New("diagnostics returned a different run or automation"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App         string                             `json:"app"`
			Name        string                             `json:"name"`
			Diagnostics api.WorkflowRunDiagnosticsResponse `json:"diagnostics"`
		}{*app, *name, diagnostics}))
	}
	if _, err := fmt.Fprintf(osStdout, "App: %s\nAutomation: %s\n", *app, *name); err != nil {
		return printErr("Could not write automation diagnostics", err)
	}
	renderWorkflowRunDiagnostics(osStdout, diagnostics)
	return 0
}
