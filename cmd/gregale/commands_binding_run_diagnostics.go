package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func showBindingRunDiagnostics(ctx context.Context, client *api.Client, app, id string) int {
	out, err := client.GetBindingVerificationRunDiagnostics(ctx, app, id)
	if err != nil {
		return printErr("Run diagnostics unavailable", err)
	}
	if out.RunID != id || out.App != app || out.CheckedAt.IsZero() {
		return printErr("Unconfirmed diagnostics", errors.New("server returned another run or app"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderRunDiagnosticFields(out.RunID, out.Status, out.Complete, out.Blockers, out.Changes, out.Recovery, out.Budget)
	return 0
}
func showProjectRunDiagnostics(ctx context.Context, client *api.Client, project, environment, id string) int {
	out, err := client.GetProjectBindingVerificationRunDiagnostics(ctx, project, environment, id)
	if err != nil {
		return printErr("Graph diagnostics unavailable", err)
	}
	if out.RunID != id || out.Project != project || out.Environment != environment || out.CheckedAt.IsZero() {
		return printErr("Unconfirmed diagnostics", errors.New("server returned another graph selection"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderRunDiagnosticFields(out.RunID, out.Status, out.Complete, out.Blockers, out.Changes, out.Recovery, out.Budget)
	return 0
}
func renderRunDiagnosticFields(id, status string, complete bool, blockers []api.BindingVerificationDiagnosticBlocker, changes []api.BindingVerificationDifference, recovery []api.BindingVerificationRecoveryAction, budget *api.BindingVerificationBudget) {
	_, _ = fmt.Fprintf(osStdout, "Verification diagnostics %s: status=%s complete=%t\n", id, status, complete)
	if budget != nil {
		_, _ = fmt.Fprintf(osStdout, "Shared budget %s: %d/%d unfinished resources, %d waiting steps\n", budget.RunID, budget.ActiveResources, budget.MaxConcurrency, budget.WaitingSteps)
	}
	for _, b := range blockers {
		_, _ = fmt.Fprintf(osStdout, "%s %s %s: %s — %s\n", b.App, b.Type, b.Binding, b.Code, b.Message)
	}
	for _, change := range changes {
		_, _ = fmt.Fprintf(osStdout, "%s %s %s: %s → %s\n", change.App, change.Binding, change.Field, change.Reviewed, change.Current)
	}
	for _, action := range recovery {
		_, _ = fmt.Fprintf(osStdout, "%s: %s\n", action.Action, action.Message)
	}
	_, _ = fmt.Fprintln(osStdout, "These observations grant no admission or activation authority; server execution rechecks current state.")
}
