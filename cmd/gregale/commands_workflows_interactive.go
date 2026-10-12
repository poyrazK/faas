package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowsInteractiveRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--interactive" || arg == "-interactive" || strings.HasPrefix(arg, "--interactive=") || strings.HasPrefix(arg, "-interactive=") {
			return true
		}
	}
	return false
}

func workflowInteractiveFlags() []cliFlag {
	return []cliFlag{
		{Name: "interactive", Bool: true, Short: "choose a run; preview and confirm changes for retry or cancel"},
		{Name: "app", Value: "slug", Short: "app for interactive run selection (otherwise use linked app or picker)"},
	}
}

type workflowInspection struct {
	run        api.WorkflowRunResponse
	steps      []api.WorkflowStepResponse
	retrySteps []api.WorkflowDiagnosticStep
}

var errWorkflowPickerClosed = errors.New("workflow picker input closed")

func workflowRunMatchesAction(run api.WorkflowRunResponse, action string) bool {
	if action == "status" {
		return true
	}
	if run.CancelledAt != nil {
		return false
	}
	if action == "retry" {
		return run.Status == "failed" || run.Status == "dead"
	}
	return run.Status == "pending" || run.Status == "running" || run.Status == "awaiting_event"
}

func chooseWorkflowRun(ctx context.Context, client *Client, prompt *startPrompt, app api.AppResponse, action string) (api.WorkflowRunResponse, bool, error) {
	offset := 0
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		page, err := client.ListWorkflowRuns(readCtx, app.Slug, 20, offset, "")
		cancel()
		if err != nil {
			return api.WorkflowRunResponse{}, false, err
		}
		if page.Total < 0 || len(page.Runs) > 20 || len(page.Runs) == 0 && page.Total > offset {
			return api.WorkflowRunResponse{}, false, errors.New("invalid workflow run pagination; rerun the command to refresh the list")
		}
		var runs []api.WorkflowRunResponse
		var labels []string
		for _, run := range page.Runs {
			if run.AppID != app.ID || !workflowUUIDPattern.MatchString(run.ID) || run.WorkflowName == "" || !api.ValidWorkflowRunStatus(run.Status) || run.Status == "" || seen[run.ID] {
				return api.WorkflowRunResponse{}, false, errors.New("workflow page has an invalid, repeated, or different app's run")
			}
			seen[run.ID] = true
			if workflowRunMatchesAction(run, action) {
				runs = append(runs, run)
				labels = append(labels, fmt.Sprintf("%s · %s · %s · %s", oneLine(run.WorkflowName), oneLine(run.Status), run.ID, oneLine(run.CreatedAt)))
			}
		}
		more := offset+len(page.Runs) < page.Total
		if len(runs) == 0 {
			PrintProgress(osStdout, "No matching workflow runs on this page.")
			if !more {
				return api.WorkflowRunResponse{}, false, nil
			}
		}
		if more {
			labels = append(labels, "Show next run page")
		}
		labels = append(labels, "Cancel")
		choice, err := prompt.choose(ctx, "Choose a workflow run for "+app.Slug+" ("+action+").", labels, len(labels)-1)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return api.WorkflowRunResponse{}, false, errWorkflowPickerClosed
			}
			return api.WorkflowRunResponse{}, false, err
		}
		if choice == len(labels)-1 {
			return api.WorkflowRunResponse{}, false, nil
		}
		if more && choice == len(runs) {
			offset += len(page.Runs)
			continue
		}
		return runs[choice], true, nil
	}
	return api.WorkflowRunResponse{}, false, errors.New("workflow run page limit reached; use workflows list with explicit pagination")
}

func sameWorkflowRunIdentity(run, selected api.WorkflowRunResponse) bool {
	return run.ID == selected.ID && run.AppID == selected.AppID && run.WorkflowName == selected.WorkflowName && run.DeploymentID == selected.DeploymentID && run.PlatformTenantID == selected.PlatformTenantID
}

func readWorkflowInspection(ctx context.Context, client *Client, selected api.WorkflowRunResponse, retry bool) (workflowInspection, error) {
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out workflowInspection
	run, err := client.GetWorkflowRun(readCtx, selected.ID)
	if err != nil {
		return out, err
	}
	if !sameWorkflowRunIdentity(run, selected) || !api.ValidWorkflowRunStatus(run.Status) || run.Status == "" {
		return out, errors.New("run identity does not match the selected app and workflow")
	}
	out.run = run
	page, err := client.ListWorkflowSteps(readCtx, run.ID)
	if err != nil {
		return out, err
	}
	seen := map[string]api.WorkflowStepResponse{}
	for _, step := range page.Steps {
		if _, duplicate := seen[step.StepName]; step.StepName == "" || duplicate || step.Attempt < 0 || step.RetryBase < 0 {
			return out, errors.New("step page contains an invalid or repeated step")
		}
		seen[step.StepName] = step
	}
	out.steps = page.Steps
	sort.Slice(out.steps, func(i, j int) bool { return out.steps[i].StepName < out.steps[j].StepName })
	if !retry {
		return out, nil
	}
	diagnostic, err := client.GetWorkflowRunDiagnostics(readCtx, run.ID)
	if err != nil {
		return out, err
	}
	if diagnostic.RunID != run.ID || diagnostic.WorkflowName != run.WorkflowName || diagnostic.Status != run.Status || diagnostic.DeploymentID != run.DeploymentID || diagnostic.LegacyUnpinned != (run.DeploymentID == "") || diagnostic.Resume.ExpectedResumeCount != run.ResumeCount || len(diagnostic.Steps) != len(out.steps) {
		return out, errors.New("workflow diagnostics changed or do not match the selected run; inspect it again")
	}
	names := map[string]bool{}
	for _, step := range diagnostic.Steps {
		current, exists := seen[step.StepName]
		if !exists || names[step.StepName] || current.Status != step.Status || current.Attempt != step.Attempt || current.RetryBase != step.RetryBase {
			return out, errors.New("workflow step diagnostics changed or do not match the selected steps")
		}
		names[step.StepName] = true
		if step.RetryEligible && (step.Status != "failed" && step.Status != "dead") {
			return out, errors.New("invalid retry preview for a step that has not failed")
		}
		for _, name := range step.RetryReopenedSteps {
			if skipped, exists := seen[name]; !exists || skipped.Status != "skipped" {
				return out, errors.New("retry preview refers to a missing or non-skipped descendant")
			}
		}
	}
	out.retrySteps = diagnostic.Steps
	sort.Slice(out.retrySteps, func(i, j int) bool { return out.retrySteps[i].StepName < out.retrySteps[j].StepName })
	return out, nil
}

func printWorkflowCommand(action string, args ...string) {
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "workflows", action)
	for _, arg := range args {
		command = append(command, quoteLogCommandArg(arg))
	}
	_, _ = fmt.Fprintln(osStdout, strings.Join(command, " "))
}

func workflowOptionalText(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return oneLine(*value)
}

func renderWorkflowInspection(slug string, inspection workflowInspection) {
	run := inspection.run
	PrintProgress(osStdout, "App: %s\nWorkflow: %s\nRun: %s\nDeployment: %s\nStatus: %s; resume count: %d\nCurrent step: %s\nStarted: %s; finished: %s\nLast error: %s", slug, oneLine(run.WorkflowName), run.ID, oneLine(workflowRunDeploymentLabel(run)), oneLine(run.Status), run.ResumeCount, workflowOptionalText(run.CurrentStep), workflowOptionalText(run.StartedAt), workflowOptionalText(run.FinishedAt), workflowOptionalText(run.LastError))
	for _, step := range inspection.steps {
		PrintProgress(osStdout, "Step %s: %s; attempt %d; error: %s", oneLine(step.StepName), oneLine(step.Status), step.Attempt, workflowOptionalText(step.Error))
	}
	if len(inspection.steps) == 0 {
		PrintProgress(osStdout, "No steps recorded.")
	}
	PrintProgress(osStdout, "Inspection commands (POSIX shells):")
	printWorkflowCommand("status", run.ID)
	printWorkflowCommand("steps", run.ID)
	printWorkflowCommand("diagnose", run.ID)
}

func cmdWorkflowsInteractive(action string, args []string) int {
	fs := newFlagSet("workflows "+action, flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a workflow run")
	appFlag := fs.String("app", "", "app slug")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 {
		return printErr("Invalid arguments", errors.New("use workflows "+action+" --interactive with optional --app; omit run IDs and step names"))
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use explicit workflow run IDs and step names for scripts"))
	}
	slug, err := resolveReadAppTarget(*appFlag)
	if err != nil {
		return readAppTargetError(err)
	}
	if !api.ValidAppSlug(slug) {
		return printErr("Invalid app", errors.New("provide a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	app, err := client.GetApp(readCtx, slug)
	cancel()
	if err != nil {
		return workflowInteractionError("Could not read app", err)
	}
	if app.Slug != slug || !workflowUUIDPattern.MatchString(app.ID) {
		return printErr("Invalid app response", errors.New("server returned another or invalid app"))
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	run, selected, err := chooseWorkflowRun(ctx, client, prompt, app, action)
	if err != nil {
		return workflowInteractionError("Could not select workflow run", err)
	}
	if !selected {
		return 0
	}
	inspection, err := readWorkflowInspection(ctx, client, run, action == "retry")
	if err != nil {
		return workflowInteractionError("Could not inspect workflow", err)
	}
	if !workflowRunMatchesAction(inspection.run, action) {
		return printErr("Workflow state changed", errors.New("choose a run again in its current state"))
	}
	renderWorkflowInspection(slug, inspection)
	if action == "status" {
		return 0
	}
	stepName := ""
	if action == "retry" {
		var eligible []api.WorkflowDiagnosticStep
		var labels []string
		for _, step := range inspection.retrySteps {
			if step.RetryEligible {
				eligible = append(eligible, step)
				labels = append(labels, fmt.Sprintf("%s · %s · attempt %d", oneLine(step.StepName), oneLine(step.Status), step.Attempt))
			}
		}
		if len(eligible) == 0 {
			PrintProgress(osStdout, "No step is eligible for manual HTTP retry in the server's current preview. Use workflows diagnose to inspect recovery options; older servers may not provide retry previews.")
			return 0
		}
		labels = append(labels, "Cancel")
		choice, err := prompt.choose(ctx, "Choose a step to retry.", labels, len(labels)-1)
		if err != nil {
			return startInputExit(err)
		}
		if choice == len(labels)-1 {
			return 0
		}
		step := eligible[choice]
		stepName = step.StepName
		PrintProgress(osStdout, "Retry step: %s; status: %s; attempt: %d\nSkipped descendants that would reopen: %s", oneLine(stepName), oneLine(step.Status), step.Attempt, oneLine(strings.Join(step.RetryReopenedSteps, ", ")))
		PrintProgress(osStdout, "The retry starts another HTTP execution and may repeat its effects. The server rechecks the captured graph, current state, and quota.")
		printWorkflowCommand("retry", run.ID, stepName)
	} else {
		PrintProgress(osStdout, "Cancellation stops further workflow progress; it does not undo completed external effects.")
		printWorkflowCommand("cancel", run.ID)
	}
	confirmed, err := prompt.confirm(ctx, "Submit this workflow "+action+"?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Workflow %s canceled. No request submitted.", action)
		return 0
	}
	latest, err := readWorkflowInspection(ctx, client, run, action == "retry")
	if err != nil {
		return workflowInteractionError("Could not recheck workflow", err)
	}
	if !reflect.DeepEqual(inspection, latest) || !workflowRunMatchesAction(latest.run, action) {
		return printErr("Workflow state changed", errors.New("run the guided command again to review the current run and steps"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var receipt api.WorkflowRunResponse
	if action == "retry" {
		receipt, err = client.RetryWorkflowStep(writeCtx, run.ID, stepName)
	} else {
		receipt, err = client.CancelWorkflowRun(writeCtx, run.ID)
	}
	if err != nil {
		return workflowInteractionError("Workflow "+action+" failed", err)
	}
	if !sameWorkflowRunIdentity(receipt, run) || !api.ValidWorkflowRunStatus(receipt.Status) || receipt.Status == "" {
		return printErr("Invalid workflow receipt", errors.New("inspect the selected run before taking further action; server returned another run or an invalid status"))
	}
	if action == "cancel" && (receipt.CancelledAt == nil || receipt.Status != "failed") {
		return printErr("Cancellation not confirmed", errors.New("the run may have finished before cancellation; inspect its current state"))
	}
	if action == "retry" && (receipt.Status != "pending" || receipt.CancelledAt != nil || receipt.CurrentStep == nil || *receipt.CurrentStep != stepName || receipt.ResumeCount != inspection.run.ResumeCount) {
		return printErr("Retry not confirmed", errors.New("server did not return the selected step requeued in the same run; inspect it before taking further action"))
	}
	PrintOK(osStdout, "Workflow %s accepted for run %s (status: %s)", action, run.ID, oneLine(receipt.Status))
	printWorkflowCommand("status", run.ID)
	return 0
}

func workflowInteractionError(title string, err error) int {
	if errors.Is(err, errWorkflowPickerClosed) {
		return startInputExit(io.EOF)
	}
	if errors.Is(err, context.Canceled) {
		return startInputExit(err)
	}
	return printErr(title, err)
}
