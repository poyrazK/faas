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

type storedWorkflowRunSummary struct {
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

func validStoredWorkflowRunSelection(app, name string) bool {
	return strings.TrimSpace(app) != "" && name != "" && strings.TrimSpace(name) == name && len(name) <= api.StoredWorkflowNameMaxBytes && !strings.ContainsAny(name, "/\\\x00")
}

func loadStoredWorkflowRunScope(ctx context.Context, client *api.Client, app, name string) (string, error) {
	application, err := client.GetApp(ctx, app)
	if err != nil {
		return "", err
	}
	if application.ID == "" {
		return "", errors.New("app lookup returned no identity")
	}
	storedWorkflow, err := client.GetStoredWorkflow(ctx, app, name)
	if err != nil {
		return "", err
	}
	if storedWorkflow.Name != name {
		return "", errors.New("stored workflow lookup returned a different name")
	}
	return application.ID, nil
}

func cmdStoredWorkflowsRuns(args []string) int {
	fs := newFlagSet("workflow stored runs", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	status := fs.String("status", "", "run status filter")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "page offset")
	after := fs.String("created-after", "", "inclusive RFC3339 creation-time start")
	before := fs.String("created-before", "", "inclusive RFC3339 creation-time end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validStoredWorkflowRunSelection(*app, *name) || *limit < 1 || *limit > 100 || *offset < 0 || !api.ValidWorkflowRunStatus(*status) {
		return printErr("Invalid stored workflow run filters", errors.New("provide --app and --name, a supported --status, --limit 1..100 and a nonnegative --offset"))
	}
	start, err := parseWorkflowRunListTime(*after, "created-after")
	if err != nil {
		return printErr("Invalid stored workflow run time filter", err)
	}
	end, err := parseWorkflowRunListTime(*before, "created-before")
	if err != nil {
		return printErr("Invalid stored workflow run time filter", err)
	}
	if start != nil && end != nil && start.After(*end) {
		return printErr("Invalid stored workflow run time range", errors.New("--created-after must not be later than --created-before"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadStoredWorkflowRunScope(ctx, client, *app, *name)
	if err != nil {
		return printErr("Could not resolve stored workflow", err)
	}
	result, err := client.ListWorkflowRunsWithOptions(ctx, *app, api.WorkflowRunListOptions{WorkflowName: *name, Status: *status, Limit: *limit, Offset: *offset, CreatedAfter: start, CreatedBefore: end})
	if err != nil {
		return printErr("Could not list stored workflow runs", err)
	}
	summaries := make([]storedWorkflowRunSummary, 0, len(result.Runs))
	for _, run := range result.Runs {
		if run.AppID != appID || run.WorkflowName != *name {
			return printErr("Stored workflow run scope mismatch", errors.New("run history returned a run outside the selected app and stored workflow"))
		}
		summaries = append(summaries, storedWorkflowRunSummary{ID: run.ID, Status: run.Status, DeploymentID: run.DeploymentID, CurrentStep: run.CurrentStep, ResumeCount: run.ResumeCount, CreatedAt: run.CreatedAt, ScheduledFor: run.ScheduledFor, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, CancelledAt: run.CancelledAt})
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App    string                     `json:"app"`
			Name   string                     `json:"name"`
			Total  int                        `json:"total"`
			Limit  int                        `json:"limit"`
			Offset int                        `json:"offset"`
			Runs   []storedWorkflowRunSummary `json:"runs"`
		}{*app, *name, result.Total, *limit, *offset, summaries}))
	}
	if _, err := fmt.Fprintf(osStdout, "Stored workflow %q in app %q: %d matching runs (offset %d, limit %d).\n", *name, *app, result.Total, *offset, *limit); err != nil {
		return printErr("Could not write stored workflow run history", err)
	}
	renderWorkflowRunsTable(osStdout, result.Runs)
	return 0
}

func cmdStoredWorkflowsDiagnose(args []string) int {
	fs := newFlagSet("workflow stored diagnose", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	runID := fs.String("run", "", "workflow run UUID")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validStoredWorkflowRunSelection(*app, *name) || !workflowUUIDPattern.MatchString(strings.ToLower(*runID)) {
		return printErr("Invalid stored workflow run selection", errors.New("provide --app, --name and --run with a valid UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadStoredWorkflowRunScope(ctx, client, *app, *name)
	if err != nil {
		return printErr("Could not resolve stored workflow", err)
	}
	run, err := client.GetWorkflowRun(ctx, *runID)
	if err != nil {
		return printErr("Could not inspect stored workflow run", err)
	}
	if !strings.EqualFold(run.ID, *runID) || run.AppID != appID || run.WorkflowName != *name {
		return printErr("Stored workflow run scope mismatch", errors.New("the run does not belong to the selected app and stored workflow"))
	}
	diagnostics, err := client.GetWorkflowRunDiagnostics(ctx, *runID)
	if err != nil {
		return printErr("Could not diagnose stored workflow run", err)
	}
	if !strings.EqualFold(diagnostics.RunID, *runID) || diagnostics.WorkflowName != *name {
		return printErr("Stored workflow diagnostics scope mismatch", errors.New("diagnostics returned a different run or stored workflow"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			App         string                             `json:"app"`
			Name        string                             `json:"name"`
			Diagnostics api.WorkflowRunDiagnosticsResponse `json:"diagnostics"`
		}{*app, *name, diagnostics}))
	}
	if _, err := fmt.Fprintf(osStdout, "App: %s\nStoredWorkflow: %s\n", *app, *name); err != nil {
		return printErr("Could not write stored workflow diagnostics", err)
	}
	renderWorkflowRunDiagnostics(osStdout, diagnostics)
	return 0
}
