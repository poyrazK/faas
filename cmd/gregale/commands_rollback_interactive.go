package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRollbackInteractive(slug string, timeout, interval time.Duration) int {
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	app, err := client.GetApp(readCtx, slug)
	if err != nil {
		return printErr("Could not read app", err)
	}
	deployments, err := client.ListAppDeploymentsAll(readCtx, slug)
	if err != nil {
		return printErr("Could not list releases", err)
	}
	var candidates []api.DeploymentResponse
	var labels []string
	for _, dep := range deployments {
		if dep.AppID != app.ID || dep.Status != "superseded" && (dep.Status != "live" || dep.TrafficPercent != 0) {
			continue
		}
		candidates = append(candidates, dep)
		labels = append(labels, fmt.Sprintf("%s · %s · %s · scope %s", deploymentLabel(dep), dep.CreatedAt, dep.Status, scopeOrDefault(dep.Scope)))
	}
	if len(candidates) == 0 {
		return printErr("No historical releases available", errors.New("no superseded or zero-traffic live releases were found for this app"))
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	selection, err := prompt.choose(ctx, "Choose a release to restore for "+slug+". The server checks artifact availability and bindings after submission.", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	target := candidates[selection]
	var current *api.DeploymentResponse
	for i := range deployments {
		dep := &deployments[i]
		if dep.AppID != app.ID || scopeOrDefault(dep.Scope) != scopeOrDefault(target.Scope) || dep.Status != "live" || dep.TrafficPercent != 100 {
			continue
		}
		if current != nil {
			return printErr("Current release is ambiguous", errors.New("multiple releases report full traffic in this scope; inspect the rollout before continuing"))
		}
		current = dep
	}
	if current == nil || current.CanaryTotalSteps > 0 && current.CanaryStep < current.CanaryTotalSteps || current.RolloutState == "rolling_out" || current.RolloutState == "aborted" {
		return printErr("No completed serving release", errors.New("this scope has no unambiguous completed release serving full traffic; inspect the rollout before continuing"))
	}
	summary, err := client.GetAppDeploymentSummary(readCtx, slug, target.ID)
	if err != nil {
		return printErr("Could not read selected release summary", err)
	}
	if summary.Deployment.AppID != app.ID || !sameBindingDeployment(summary.Deployment.ID, target.ID) || scopeOrDefault(summary.Deployment.Scope) != scopeOrDefault(target.Scope) {
		return printErr("Invalid release summary", errors.New("the summary does not match the selected app, deployment, and scope"))
	}
	_, _ = fmt.Fprintf(osStdout, "App: %s; scope: %s\nCurrent: %s (%s)\nRestore: %s (%s)\n", slug, scopeOrDefault(target.Scope), deploymentLabel(*current), current.ID, deploymentLabel(target), target.ID)
	_, _ = fmt.Fprintln(osStdout, "Selected release summary (changes relative to its preceding release):")
	if summary.Previous == nil {
		_, _ = fmt.Fprintln(osStdout, "  Initial release; no preceding release to compare.")
	} else if len(summary.Changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "  No recorded changes relative to the preceding release.")
	} else {
		for _, change := range summary.Changes {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s -> %s\n", change.Field, formatSummaryValue(change.Before), formatSummaryValue(change.After))
		}
	}
	var reason string
	for {
		reason, err = prompt.text(ctx, "Reason (optional, one line, at most 256 bytes)", "")
		if err != nil {
			return startInputExit(err)
		}
		if len(reason) <= api.BindingReleasePolicyReasonMaxBytes && utf8.ValidString(reason) && strings.IndexFunc(reason, unicode.IsControl) < 0 {
			break
		}
		_, _ = fmt.Fprintln(prompt.writer, "Use one line of at most 256 bytes without control characters.")
	}
	confirmed, err := prompt.confirm(ctx, "Restore "+deploymentLabel(target)+" in scope "+scopeOrDefault(target.Scope)+" for "+slug+"?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No rollback submitted.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	// UUIDs pin both releases even if a new deployment starts while the user
	// reviews the summary. The checked endpoint rejects a changed current pair.
	return cmdCheckedRollback(slug, target.ID, current.ID, reason, true, timeout, interval)
}
