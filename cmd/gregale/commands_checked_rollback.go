package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const rollbackStatusUsage = "usage: gregale rollback status <slug> --operation UUID [--wait] [--timeout 10m] [--poll-interval 2s]"

type rollbackOperationClient interface {
	GetRollbackOperation(context.Context, string, string) (api.RollbackOperation, error)
}

func rollbackReceiptMatches(got, pin api.RollbackOperation) bool {
	return sameBindingDeployment(got.ID, pin.ID) && got.AppID == pin.AppID && got.Scope == pin.Scope && sameBindingDeployment(got.TargetDeploymentID, pin.TargetDeploymentID) && sameBindingDeployment(got.CurrentDeploymentID, pin.CurrentDeploymentID) && got.Service == pin.Service
}
func pollRollbackOperation(ctx context.Context, client rollbackOperationClient, slug string, pin api.RollbackOperation, wait bool, interval time.Duration) (api.RollbackOperation, error) {
	var last api.RollbackOperation
	for {
		got, err := client.GetRollbackOperation(ctx, slug, pin.ID)
		if err != nil {
			return last, err
		}
		if !rollbackReceiptMatches(got, pin) {
			return last, fmt.Errorf("server returned a different rollback operation or deployment pair")
		}
		switch got.Status {
		case "preparing", "ready", "blocked", "routing", "complete", "failed":
		default:
			return last, fmt.Errorf("server returned an unknown rollback state")
		}
		last = got
		if got.Status == "failed" {
			return last, fmt.Errorf("rollback failed: %s", got.Code)
		}
		if got.Status == "complete" {
			if got.AuditID == "" || got.CompletedAt == nil {
				return last, fmt.Errorf("rollback completion is missing its committed receipt")
			}
			return last, nil
		}
		if !wait {
			return last, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}
func cmdCheckedRollback(slug, target, current, reason string, wait bool, timeout, interval time.Duration) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Read app failed", err)
	}
	targetID, err := resolveBindingDeployment(ctx, client, slug, target)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Resolve rollback target failed", err)
	}
	currentID, err := resolveBindingDeployment(ctx, client, slug, current)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Resolve current deployment failed", err)
	}
	if sameBindingDeployment(targetID, currentID) {
		return printErr("Invalid rollback", fmt.Errorf("target and expected current deployment must differ"))
	}
	response, err := client.CheckedRollback(ctx, slug, api.RollbackRequest{TargetDeploymentID: &targetID, ExpectedCurrentDeploymentID: &currentID, Reason: reason})
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Rollback request failed", err)
	}
	operation := response.RollbackOperation
	if operation == nil || operation.ID == "" || operation.Scope == "" || operation.Scope != response.Scope || operation.CreatedAt.IsZero() || operation.UpdatedAt.IsZero() || operation.AppID != app.ID || !sameBindingDeployment(response.ID, targetID) || response.AppID != app.ID || !sameBindingDeployment(operation.TargetDeploymentID, targetID) || !sameBindingDeployment(operation.CurrentDeploymentID, currentID) || operation.Status != "preparing" {
		return printErr("Invalid rollback receipt", fmt.Errorf("server did not confirm durable intent for the exact deployment pair"))
	}
	if _, err := uuid.Parse(operation.ID); err != nil {
		return printErr("Invalid rollback receipt", err)
	}
	if !wait {
		return outputRollbackOperation(*operation)
	}
	last, err := pollRollbackOperation(ctx, client, slug, *operation, true, interval)
	if last.ID != "" {
		if code := outputRollbackOperation(last); code != 0 {
			return code
		}
	} else {
		_ = outputRollbackOperation(*operation)
	}
	return checkedRollbackCLIError(signalCtx, "Rollback wait failed", err)
}
func checkedRollbackCLIError(ctx context.Context, title string, err error) int {
	if ctx.Err() != nil {
		return 130
	}
	if err == nil {
		return 0
	}
	return printErr(title, err)
}
func outputRollbackOperation(r api.RollbackOperation) int {
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	_, _ = fmt.Fprintf(osStdout, "Rollback %s: %s\n  target: %s\n  expected current: %s\n", r.ID, r.Status, r.TargetDeploymentID, r.CurrentDeploymentID)
	if r.Code != "" {
		_, _ = fmt.Fprintf(osStdout, "  blocked: %s\n", r.Code)
	}
	for _, b := range r.Blockers {
		_, _ = fmt.Fprintf(osStdout, "    %s: %s\n", b.Code, b.Message)
	}
	if r.AuditID != "" {
		_, _ = fmt.Fprintf(osStdout, "  routing audit: %s\n", r.AuditID)
	}
	return 0
}
func cmdRollbackStatus(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rollbackStatusUsage, "rollback")
		return 0
	}
	fs := newFlagSet("rollback status", flag.ContinueOnError)
	id := fs.String("operation", "", "rollback operation UUID")
	wait := fs.Bool("wait", false, "wait for completion")
	timeout := fs.Duration("timeout", 10*time.Minute, "wait deadline")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval")
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	if fs.Parse(args) != nil {
		return 1
	}
	if slug == "" && fs.NArg() == 1 {
		slug = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return 1
	}
	operationID, err := uuid.Parse(*id)
	if err != nil || slug == "" || *timeout <= 0 || *interval <= 0 {
		PrintUsage(os.Stderr, rollbackStatusUsage, "rollback")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Read app failed", err)
	}
	pin, err := client.GetRollbackOperation(ctx, slug, operationID.String())
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Read rollback failed", err)
	}
	if !sameBindingDeployment(pin.ID, operationID.String()) || pin.AppID != app.ID || pin.TargetDeploymentID == "" || pin.CurrentDeploymentID == "" || pin.Scope == "" {
		return printErr("Invalid rollback receipt", errors.New("server returned a different rollback operation or app"))
	}
	last, err := pollRollbackOperation(ctx, client, slug, pin, *wait, *interval)
	if last.ID != "" {
		if code := outputRollbackOperation(last); code != 0 {
			return code
		}
	}
	return checkedRollbackCLIError(signalCtx, "Rollback status failed", err)
}
