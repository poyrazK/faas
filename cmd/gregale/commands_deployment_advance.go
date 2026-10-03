package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdDeploymentAdvance(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("deployment advance", flag.ContinueOnError)
	step := fs.Int("expected-step", -1, "current observed canary step")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !deploymentIDPattern.MatchString(positional[0]) || *step < 0 {
		return printErr("Invalid canary advance", errors.New("usage: gregale deployment advance <ID> --expected-step N"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	result, err := client.AdvanceCanary(ctx, positional[0], *step)
	if err != nil {
		return printErr("Could not advance canary", err)
	}
	want, _ := uuid.Parse(positional[0])
	got, parseErr := uuid.Parse(result.Deployment.ID)
	if parseErr != nil || got != want || result.Deployment.CanaryStep <= *step || result.AuditID == "" {
		return printErr("Invalid canary result", errors.New("deployment, step or audit does not match the advance"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Advanced canary %s to step %d at %d%% traffic\n", result.Deployment.ID, result.Deployment.CanaryStep, result.Deployment.TrafficPercent)
	if gate := result.RouteGate; gate != nil {
		_, _ = fmt.Fprintf(osStdout, "Route gate: %s (%s)\n", previewReportText(gate.Mode), previewReportText(gate.Status))
		if len(gate.Reasons) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Route findings: %s\n", previewReportText(strings.Join(gate.Reasons, ", ")))
		}
	}
	if health := result.RouteHealth; health != nil {
		_, _ = fmt.Fprintf(osStdout, "Route health: %s (%s): %s\n", previewReportText(health.Mode), previewReportText(health.Status), previewReportText(health.Reason))
		if health.HistoryID != "" {
			_, _ = fmt.Fprintf(osStdout, "Saved health decision: %s (routes health explain APP --deployment %s --decision %s)\n", previewReportText(health.HistoryID), result.Deployment.ID, previewReportText(health.HistoryID))
		}
	}
	return 0
}
