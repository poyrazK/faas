package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowReadTarget struct{ Ref, App, Name string }

func parseWorkflowReadTarget(verb string, args []string) (workflowReadTarget, int) {
	fs := newFlagSet("workflows "+verb, flag.ContinueOnError)
	app := fs.String("app", "", "app slug for latest (defaults to linked project app)")
	name := fs.String("workflow-name", "", "exact workflow name for latest (inferred when the app's recent runs share one)")
	if err := parseInterspersed(fs, args); err != nil {
		return workflowReadTarget{}, 1
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "latest" && !workflowUUIDPattern.MatchString(fs.Arg(0))) {
		return workflowReadTarget{}, printErr("Invalid workflow read target", fmt.Errorf("usage: gregale workflows %s RUN_ID | latest [--workflow-name NAME] [--app SLUG]", verb))
	}
	return validateWorkflowReadTarget(fs.Arg(0), *app, *name)
}

func validateWorkflowReadTarget(ref, app, name string) (workflowReadTarget, int) {
	target := workflowReadTarget{Ref: ref, App: app, Name: name}
	if target.Ref != "latest" {
		if app != "" || name != "" {
			return workflowReadTarget{}, printErr("Invalid workflow read options", errors.New("--app and --workflow-name select latest; an explicit run ID needs neither"))
		}
		return target, 0
	}
	if name != "" && (strings.TrimSpace(name) == "" || len(name) > api.WorkflowWebhookNameMaxBytes) {
		return workflowReadTarget{}, printErr("Invalid workflow name", fmt.Errorf("--workflow-name must be non-empty and at most %d bytes", api.WorkflowWebhookNameMaxBytes))
	}
	resolved, err := resolveWorkflowApp(app)
	if err != nil {
		return workflowReadTarget{}, printErr("Could not resolve workflow app", err)
	}
	target.App = resolved
	return target, 0
}

// Read commands resolve latest once. Lookup is filtered on the server,
// so the first row is the newest run for this exact app/workflow pair.
func resolveWorkflowReadTarget(ctx context.Context, client *api.Client, target workflowReadTarget) (string, error) {
	if target.Ref != "latest" {
		return target.Ref, nil
	}
	if target.Name == "" {
		name, err := inferLatestWorkflowName(ctx, client, target.App)
		if err != nil {
			return "", err
		}
		target.Name = name
	}
	page, err := client.ListWorkflowRunsWithOptions(ctx, target.App, api.WorkflowRunListOptions{Limit: 1, WorkflowName: target.Name})
	if err != nil {
		return "", err
	}
	if len(page.Runs) == 0 {
		return "", fmt.Errorf("no runs found for workflow %q in app %q; review %s workflows list --app %s --workflow-name %s before submitting a run", target.Name, target.App, recoveryCLI(), recoveryArgument(target.App), recoveryArgument(target.Name))
	}
	run := page.Runs[0]
	if !workflowUUIDPattern.MatchString(run.ID) || run.WorkflowName != target.Name {
		return "", errors.New("latest workflow response did not identify a valid run for the exact workflow name")
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Selected latest workflow run: app=%s; workflow=%s; run=%s; status=%s.\n", compactInspectFailureText(target.App), compactInspectFailureText(target.Name), run.ID, compactInspectFailureText(run.Status))
		_, _ = fmt.Fprintln(osStderr, "Latest means newest for this app and workflow name, including unfinished or failed runs. This read uses the selected ID.")
	}
	return run.ID, nil
}

// latestWorkflowNameScan bounds the recent runs read to infer a workflow name.
const latestWorkflowNameScan = 100

// inferLatestWorkflowName returns the workflow name shared by the app's recent
// runs. `latest` needed --workflow-name even for an app with one workflow
// (Hunt 11 H11-29); an app whose recent runs span several workflows still has
// to name one, so latest never silently picks another workflow's run.
func inferLatestWorkflowName(ctx context.Context, client *api.Client, app string) (string, error) {
	page, err := client.ListWorkflowRunsWithOptions(ctx, app, api.WorkflowRunListOptions{Limit: latestWorkflowNameScan})
	if err != nil {
		return "", err
	}
	var names []string
	seen := map[string]bool{}
	for _, run := range page.Runs {
		if run.WorkflowName != "" && !seen[run.WorkflowName] {
			seen[run.WorkflowName] = true
			names = append(names, run.WorkflowName)
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no workflow runs found in app %q; pass --workflow-name or start a run first", app)
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("app %q has runs of several workflows (%s); pass --workflow-name", app, strings.Join(names, ", "))
	}
}
