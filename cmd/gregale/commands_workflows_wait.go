package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowsWait(args []string) int {
	fs := newFlagSet("workflows wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum wait")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval (1s to 1h)")
	app := fs.String("app", "", "app slug for latest (defaults to linked project app)")
	name := fs.String("workflow-name", "", "exact workflow name required for latest")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "latest" && !workflowUUIDPattern.MatchString(fs.Arg(0))) || *timeout <= 0 || *interval < time.Second || *interval > time.Hour {
		return printErr("Invalid workflow wait options", errors.New("usage: gregale workflow wait RUN_ID|latest [--workflow-name NAME] [--app SLUG] [--timeout 5m] [--poll-interval 2s]; provide a UUID, a positive timeout, and a poll interval between 1s and 1h"))
	}
	target, code := validateWorkflowReadTarget(fs.Arg(0), *app, *name)
	if code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lookupCtx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	runID, err := resolveWorkflowReadTarget(lookupCtx, client, target)
	if err != nil {
		return printErr("Could not resolve workflow run", err)
	}
	return waitWorkflowRun(client, runID, *timeout, *interval, nil)
}

func waitWorkflowRun(client *api.Client, runID string, timeout, interval time.Duration, initial *api.WorkflowRunResponse) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	last := initial
	resume := workflowWaitResumeCommand(runID, timeout, interval)
	stopWait := func(title string, err error) int {
		if initial == nil && !jsonOutput {
			return printErr(title, err)
		}
		if last != nil {
			if code := renderWorkflowRunStatus(*last); code != 0 {
				return code
			}
		}
		if jsonOutput {
			return printWorkflowWaitError(title, err, runID, timeout, interval)
		}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			problem := apiErr.Problem
			problem.Hint += " Resume waiting with: " + resume
			err = &api.APIError{Problem: problem}
		} else {
			err = fmt.Errorf("%w; the run was accepted; resume waiting with: %s", err, resume)
		}
		code := printErr(title, err)
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Accepted run: %s. Resume reading this run with:\n  %s\n", runID, resume)
		}
		return code
	}
	previous := ""
	next := initial
	for ctx.Err() == nil {
		var run api.WorkflowRunResponse
		var err error
		if next != nil {
			run, next = *next, nil
		} else {
			readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
			run, err = client.GetWorkflowRun(readCtx, runID)
			readCancel()
		}
		if ctx.Err() != nil {
			break
		}
		progress := "Workflow status unavailable; retrying."
		if err != nil {
			if inspectWatchPermanentError(err) {
				return stopWait("Workflow wait failed", err)
			}
		} else {
			if run.ID != runID {
				return stopWait("Workflow wait failed", errors.New("status response did not identify the requested run"))
			}
			last = &run
			if run.CancelledAt != nil || run.Status == "succeeded" || run.Status == "failed" || run.Status == "dead" {
				if code := renderWorkflowRunStatus(run); code != 0 {
					return code
				}
				if run.Status == "succeeded" && run.CancelledAt == nil {
					return 0
				}
				return printWorkflowWaitError("Workflow did not succeed", fmt.Errorf("run %s finished with status %s (cancelled=%t); inspect its details before retrying", runID, compactInspectFailureText(run.Status), run.CancelledAt != nil), runID, timeout, interval)
			}
			if run.Status != "pending" && run.Status != "running" && run.Status != "awaiting_event" {
				if code := renderWorkflowRunStatus(run); code != 0 {
					return code
				}
				return printWorkflowWaitError("Unknown workflow state", errors.New("the returned status is not recognized; success cannot be confirmed"), runID, timeout, interval)
			}
			step := ""
			if run.CurrentStep != nil {
				step = compactInspectFailureText(*run.CurrentStep)
			}
			progress = fmt.Sprintf("Workflow %s: %s; step=%s; resumes=%d", runID, run.Status, step, run.ResumeCount)
		}
		if !jsonOutput && previous != progress {
			if _, err := fmt.Fprintln(osStderr, progress); err != nil {
				return printErr("Could not write workflow progress", err)
			}
		}
		previous = progress
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if last != nil {
		if code := renderWorkflowRunStatus(*last); code != 0 {
			return code
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		if jsonOutput {
			detail := "Stopping this wait does not cancel the run."
			if initial != nil {
				detail = "The run was accepted; stopping this wait does not cancel it."
			}
			return renderWorkflowWaitProblem(api.Problem{Code: "workflow_wait_interrupted", Title: "Workflow wait canceled", Status: 499, Detail: detail, DocsURL: cliDocsURL}, 130, runID, timeout, interval)
		}
		if initial != nil {
			_, _ = fmt.Fprintf(osStderr, "Wait canceled; accepted run %s remains unchanged.\nResume waiting:\n  %s\n", runID, resume)
			return 130
		}
		_, _ = fmt.Fprintf(osStderr, "Wait canceled. Resume waiting:\n  %s\n", resume)
		return 130
	}
	message := "Workflow wait timed out; the run may still be active. Any returned status is the last observation."
	if jsonOutput {
		return renderWorkflowWaitProblem(api.Problem{Type: docsSiteURL + "/errors/invalid-request", Code: "workflow_wait_timeout", Title: "Workflow wait timed out", Status: 408, Detail: message, DocsURL: cliDocsURL}, 124, runID, timeout, interval)
	} else {
		_, _ = fmt.Fprintf(osStderr, "%s\nResume waiting (POSIX shells):\n  %s\n", message, resume)
	}
	return 124
}
