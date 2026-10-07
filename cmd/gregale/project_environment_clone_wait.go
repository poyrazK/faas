package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var projectEnvironmentClonePollInterval = 2 * time.Second

type projectEnvironmentCloneWaitReceipt struct {
	Clone         api.ProjectEnvironmentCloneOperationResponse `json:"clone_operation"`
	Succeeded     bool                                         `json:"succeeded"`
	TimedOut      bool                                         `json:"timed_out,omitempty"`
	ResumeCommand string                                       `json:"resume_command,omitempty"`
}

func envCloneStatus(args []string) int {
	flags, positional := splitArgsForFlags(args, "wait")
	fs := newFlagSet("env-clone-status", flag.ContinueOnError)
	project := fs.String("project", "", "project slug (defaults to linked project)")
	wait := fs.Bool("wait", false, "wait for the clone to finish")
	timeout := secondsOrDurationFlag(fs, "timeout", defaultDeployWaitTimeoutSeconds, "maximum wait (seconds or a duration such as 10m)")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 || *timeout <= 0 || *timeout > 24*60*60 {
		PrintUsage(os.Stderr, "usage: gregale env clone-status <operation-id> [--project <slug>] [--wait] [--timeout SECONDS]", "env")
		return 1
	}
	projectSlug, err := environmentProjectSlug(*project)
	if err != nil {
		return printErr("Could not resolve project", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	op, err := client.GetProjectEnvironmentCloneOperation(context.Background(), projectSlug, positional[0])
	if err != nil {
		return printErr("Clone status failed", err)
	}
	if op.OperationID != positional[0] || op.ProjectSlug != projectSlug {
		return printErr("Clone status failed", errors.New("server returned a different clone operation"))
	}
	if *wait {
		return finishProjectEnvironmentCloneWait(context.Background(), client, op, time.Duration(*timeout)*time.Second)
	}
	return renderProjectEnvironmentCloneOperation(op, false)
}

func finishProjectEnvironmentCloneWait(ctx context.Context, client *Client, initial api.ProjectEnvironmentCloneOperationResponse, timeout time.Duration) int {
	op, timedOut, err := waitForProjectEnvironmentClone(ctx, client, initial, timeout)
	if err != nil {
		return printErr("Clone wait failed", err)
	}
	return renderProjectEnvironmentCloneOperation(op, timedOut)
}

func waitForProjectEnvironmentClone(ctx context.Context, client *Client, initial api.ProjectEnvironmentCloneOperationResponse, timeout time.Duration) (api.ProjectEnvironmentCloneOperationResponse, bool, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	op := initial
	for {
		switch op.Status {
		case "ready", "failed", "compensated":
			return op, false, nil
		case "pending", "capturing", "copying", "publishing", "compensating":
		default:
			return op, false, fmt.Errorf("unknown clone status %q", op.Status)
		}
		current, err := client.GetProjectEnvironmentCloneOperation(waitCtx, initial.ProjectSlug, initial.OperationID)
		if err != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return op, true, nil
			}
			return op, false, err
		}
		if current.OperationID != initial.OperationID || current.ProjectSlug != initial.ProjectSlug || current.SourceEnvironment != initial.SourceEnvironment ||
			current.TargetEnvironment != initial.TargetEnvironment || current.SourceRevisionHash != initial.SourceRevisionHash || current.Revision < op.Revision {
			return op, false, errors.New("clone status changed its captured identity or moved backwards")
		}
		op = current
		if op.Status == "ready" || op.Status == "failed" || op.Status == "compensated" {
			continue
		}
		timer := time.NewTimer(projectEnvironmentClonePollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return op, true, nil
			}
			return op, false, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func renderProjectEnvironmentCloneOperation(op api.ProjectEnvironmentCloneOperationResponse, timedOut bool) int {
	receipt := projectEnvironmentCloneWaitReceipt{Clone: op, Succeeded: op.Status == "ready", TimedOut: timedOut}
	if timedOut || op.Status != "ready" && op.Status != "compensated" {
		receipt.ResumeCommand = fmt.Sprintf("gregale env clone-status %s --project %s --wait", op.OperationID, op.ProjectSlug)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Clone %s: %s -> %s (%s)\n", op.OperationID, op.SourceEnvironment, op.TargetEnvironment, op.Status)
		for _, resource := range op.Resources {
			_, _ = fmt.Fprintf(osStdout, "  %s %s: %s\n", resource.Kind, resource.Name, resource.Status)
		}
		if op.ErrorCode != "" {
			_, _ = fmt.Fprintf(osStdout, "  error: %s\n", op.ErrorCode)
		}
		if receipt.ResumeCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "  resume: %s\n", receipt.ResumeCommand)
		}
	}
	if timedOut {
		return 3
	}
	if op.Status == "failed" || op.Status == "compensated" {
		return 1
	}
	return 0
}
