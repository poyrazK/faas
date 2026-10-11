package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const alertActionsUsage = "usage: gregale alerts actions --app <slug> [--fire UUID [--wait] [--timeout 10m] [--poll-interval 2s]]"

type alertRollbackClient interface {
	GetAlertRollback(context.Context, string, string) (api.AlertRollback, error)
}

func alertActionReceiptMatches(got, pin api.AlertRollback) bool {
	return sameBindingDeployment(got.ID, pin.ID) && got.RuleID == pin.RuleID && got.AccountID == pin.AccountID && got.AppID == pin.AppID && got.Scope == pin.Scope && got.CandidateDeploymentID == pin.CandidateDeploymentID && got.PredecessorDeploymentID == pin.PredecessorDeploymentID && got.Service == pin.Service && got.Historical == pin.Historical
}

func alertActionFinished(r api.AlertRollback) (bool, error) {
	switch r.Status {
	case "pending", "blocked":
		return false, nil
	case "failed":
		return true, fmt.Errorf("alert rollback failed: %s", r.Code)
	case "complete":
		if e := r.DeploymentEvidence; e != nil && (e.Status != "breached" || e.DeploymentID != r.CandidateDeploymentID) {
			return true, fmt.Errorf("historical alert rollback completion is missing qualified deployment evidence")
		}
		if r.Historical && (r.RollbackOperationID != r.ID || r.RollbackPhase != "complete" || r.RollbackRoutingAuditID == "") {
			return true, fmt.Errorf("historical alert rollback completion is missing its committed operation or audit")
		}
		if r.CompletedAt == nil || r.CompletedAt.IsZero() || r.AuditID == "" || r.Service && !r.Historical && (r.ServiceRequestID != r.ID || r.ServicePhase != "complete" || r.ServiceRoutingAuditID == "") {
			return true, fmt.Errorf("alert rollback completion is missing its committed handoff or audit")
		}
		return true, nil
	default:
		return true, fmt.Errorf("server returned an unknown alert action state")
	}
}

func waitAlertRollback(ctx context.Context, client alertRollbackClient, slug string, pin api.AlertRollback, interval time.Duration) (api.AlertRollback, error) {
	last := pin
	for {
		if done, err := alertActionFinished(last); done {
			return last, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
		got, err := client.GetAlertRollback(ctx, slug, pin.ID)
		if err != nil {
			return last, err
		}
		if !alertActionReceiptMatches(got, pin) || got.ServiceRequestID != "" && got.ServiceRequestID != pin.ID || last.ServiceRequestID != "" && got.ServiceRequestID != last.ServiceRequestID || got.RollbackOperationID != "" && got.RollbackOperationID != pin.ID || last.RollbackOperationID != "" && got.RollbackOperationID != last.RollbackOperationID {
			return last, fmt.Errorf("server returned a different alert action, deployment pair or service request")
		}
		if !alertEvidenceReceiptMatches(got, last) {
			return last, fmt.Errorf("server returned different deployment evidence")
		}
		last = got
	}
}

func alertEvidenceReceiptMatches(got, last api.AlertRollback) bool {
	a, b := got.DeploymentEvidence, last.DeploymentEvidence
	if b == nil {
		return true // Receipts accepted before deployment qualification remain readable.
	}
	if a == nil || last.RollbackOperationID != "" && !reflect.DeepEqual(a, b) {
		return false
	}
	return a.Version == b.Version && a.DeploymentID == b.DeploymentID && a.Metric == b.Metric &&
		a.Comparison == b.Comparison && a.Threshold == b.Threshold && a.WindowSpec == b.WindowSpec &&
		a.CutoverAt.Equal(b.CutoverAt) && a.WindowStart.Equal(b.WindowStart) && a.WindowEnd.Equal(b.WindowEnd)
}

func cmdWaitAlertRollback(client *api.Client, slug, fire string, timeout, interval time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Read app failed", err)
	}
	pin, err := client.GetAlertRollback(ctx, slug, fire)
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Read alert action failed", err)
	}
	if !sameBindingDeployment(pin.ID, fire) || pin.AppID != app.ID || pin.ServiceRequestID != "" && pin.ServiceRequestID != fire || pin.RollbackOperationID != "" && pin.RollbackOperationID != fire {
		return printErr("Invalid alert action receipt", fmt.Errorf("server returned a different alert fire, app or service request"))
	}
	last, err := waitAlertRollback(ctx, client, slug, pin, interval)
	if jsonOutput {
		if code := jsonOut(writeJSON(last)); code != 0 {
			return code
		}
	} else if code := outputAlertRollback(last); code != 0 {
		return code
	}
	return checkedRollbackCLIError(signalCtx, "Alert action wait failed", err)
}
