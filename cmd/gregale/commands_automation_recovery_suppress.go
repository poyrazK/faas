package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
)

func cmdAutomationsRecoverySuppress(args []string) int {
	fs := newFlagSet("automations recovery-suppress", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	review := fs.String("review", "", "review ID")
	action := fs.String("action", "", "queued action ID")
	version := fs.Int64("policy-version", 0, "observed policy version")
	reason := fs.String("reason", "", "operator suppression reason")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	req := api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: *version, Reason: *reason}
	if strings.TrimSpace(*app) == "" || !workflowUUIDPattern.MatchString(*review) || !workflowUUIDPattern.MatchString(*action) {
		return printErr("Invalid suppression scope", errors.New("provide --app, --review and --action"))
	}
	if err := req.Validate(); err != nil {
		return printErr("Invalid automatic suppression", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.SuppressWorkflowRecoveryAutomaticAction(ctx, *app, *review, *action, req)
	if err != nil {
		return printErr("Could not suppress automatic retry", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Action %s | %s | %s\n", oneLine(out.ActionID), oneLine(out.Status), oneLine(out.Reason))
	if out.Suppression != nil {
		_, _ = fmt.Fprintf(osStdout, "Suppressed by %s %s | %s\n", oneLine(out.Suppression.ActorKind), oneLine(out.Suppression.ActorID), oneLine(out.Suppression.Reason))
	}
	return 0
}
