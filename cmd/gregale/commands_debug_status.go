package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDebugStatus answers "is my app OK, and if not where do I look?" in one
// command: health over the window, serving deployments, the release in flight
// or rolled back, open regressions, recent failures, and next commands.
func cmdDebugStatus(args []string) int {
	fs := newFlagSet("debug status", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (default 1h; e.g. 30m, 24h)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"since": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug status [--since D] <slug>", debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugStatus(context.Background(), positional[0], *since)
	if err != nil {
		return printErr("Could not get debug status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugStatus(osStdout, positional[0], resp)
	return 0
}

// cmdDebugRollout explains why the app's newest canary rollout (or
// --deployment-id) was rolled back or is held, with the evidence behind it.
func cmdDebugRollout(args []string) int {
	fs := newFlagSet("debug rollout", flag.ContinueOnError)
	deploymentID := fs.String("deployment-id", "", "deployment to explain (default: the newest canary rollout)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"deployment-id": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug explain [--deployment-id UUID] <slug>", debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugRollout(context.Background(), positional[0], *deploymentID)
	if err != nil {
		return printErr("Could not explain the rollout", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRollout(osStdout, positional[0], resp)
	return 0
}

func renderDebugStatus(w io.Writer, slug string, resp api.DebugStatusResponse) {
	_, _ = fmt.Fprintf(w, "%s: %s\n  %s\n\n", slug, strings.ReplaceAll(resp.Verdict, "_", " "), resp.Headline)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Health (last %s)\t%d requests, %d server errors, p95 %d ms\n",
		resp.Since, resp.Health.Requests, resp.Health.ServerErrors, resp.Health.P95MS)
	label := "Serving"
	for _, dep := range resp.Deployments {
		line := fmt.Sprintf("%s  %d%%", debugShortID(dep.ID), dep.TrafficPercent)
		if dep.CommitSHA != "" {
			line += "  commit " + debugShortID(dep.CommitSHA)
		}
		if dep.RolloutState == api.DebugRolloutRollingOut || dep.RolloutState == "pending" {
			line += "  (rolling out)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", label, line)
		label = ""
	}
	if resp.Rollout != nil {
		_, _ = fmt.Fprintf(tw, "Release\t%s\n", resp.Rollout.Headline)
	}
	label = "Regressions"
	for _, reg := range resp.Regressions {
		_, _ = fmt.Fprintf(tw, "%s\t%s  %s  deployment %s\n", label, reg.Route, debugRegressionSignal(reg), debugShortID(reg.DeploymentID))
		label = ""
	}
	if more := resp.RegressionsTotal - len(resp.Regressions); more > 0 {
		_, _ = fmt.Fprintf(tw, "\t... and %d more\n", more)
	}
	label = "Out of memory"
	for _, kill := range resp.OOMKills {
		_, _ = fmt.Fprintf(tw, "%s\t%s  %s  deployment %s%s\n", label, debugClock(kill.At), debugOOMPeak(kill), debugShortID(kill.DeploymentID), debugOOMOutcome(kill))
		label = ""
	}
	if more := resp.OOMKillsTotal - len(resp.OOMKills); more > 0 {
		_, _ = fmt.Fprintf(tw, "\t... and %d more\n", more)
	}
	label = "Recent failures"
	for _, req := range resp.RecentFailures {
		_, _ = fmt.Fprintf(tw, "%s\t%s  %s  %d  %s\n", label, debugClock(req.ReceivedAt), requestLine(req.Method, req.Route), req.Status, debugRequestRef(req))
		label = ""
	}
	tracing := "on"
	if !resp.TracingEnabled {
		tracing = "off (set tracing.enabled: true for spans and request logs)"
	}
	_, _ = fmt.Fprintf(tw, "Tracing\t%s\n", tracing)
	_ = tw.Flush()
	if resp.OOMFix != "" {
		_, _ = fmt.Fprintln(w, "\nMemory:")
		for _, line := range strings.Split(resp.OOMFix, "\n") {
			_, _ = fmt.Fprintf(w, "  %s\n", line)
		}
	}
	next := debugStatusNext(slug, resp)
	if len(next) > 0 {
		_, _ = fmt.Fprintln(w, "\nNext:")
		for _, cmd := range next {
			_, _ = fmt.Fprintf(w, "  %s\n", cmd)
		}
	}
}

// debugStatusNext suggests the commands that drill into what the status
// found, most specific first.
func debugStatusNext(slug string, resp api.DebugStatusResponse) []string {
	var next []string
	if resp.Rollout != nil && resp.Rollout.State != api.DebugRolloutRollingOut {
		next = append(next, "gregale debug explain "+slug)
	}
	if len(resp.RecentFailures) > 0 {
		next = append(next, "gregale debug requests inspect "+slug+" "+debugRequestRef(resp.RecentFailures[0]))
	}
	if resp.RegressionsTotal > 0 {
		next = append(next, "gregale debug regressions "+slug)
	}
	if resp.OOMKillsTotal > 0 {
		next = append(next, "gregale logs "+slug)
	}
	return next
}

func debugOOMPeak(kill api.DebugStatusOOMKill) string {
	switch {
	case kill.PeakMB > 0 && kill.PlanMB > 0:
		return fmt.Sprintf("peak %d MB, plan %d MB", kill.PeakMB, kill.PlanMB)
	case kill.PlanMB > 0:
		return fmt.Sprintf("plan %d MB", kill.PlanMB)
	}
	return "killed"
}

// debugOOMOutcome says what the kill did to its deployment: a deployment that
// was not yet live failed; a live release kept serving on a new instance.
func debugOOMOutcome(kill api.DebugStatusOOMKill) string {
	if kill.DeploymentFailed {
		return "  (deployment failed)"
	}
	return "  (instance replaced)"
}

func renderDebugRollout(w io.Writer, slug string, resp api.DebugRolloutExplanation) {
	_, _ = fmt.Fprintln(w, resp.Headline)
	if resp.DeploymentID == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "Deployment %s", resp.DeploymentID)
	if resp.StableDeploymentID != "" {
		_, _ = fmt.Fprintf(w, " vs stable %s", resp.StableDeploymentID)
	}
	_, _ = fmt.Fprintln(w)
	if resp.WindowStart != "" {
		clamped := ""
		if resp.RetentionClamped {
			clamped = " (shortened to the plan's debugger retention)"
		}
		_, _ = fmt.Fprintf(w, "Window %s to %s%s\n", resp.WindowStart, resp.WindowEnd, clamped)
	}
	if d := resp.Decision; d != nil {
		_, _ = fmt.Fprintf(w, "Decision %s (%s) at step %d, %d%% traffic\n", d.Rule, d.Kind, d.Step+1, d.TrafficPercent)
	}
	if len(resp.Routes) > 0 {
		_, _ = fmt.Fprintln(w, "\nRoutes (this release vs stable):")
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  ROUTE\tREQUESTS\tSERVER ERRORS\tP95\tSTABLE ERRORS\tSTABLE P95")
		for _, route := range resp.Routes {
			_, _ = fmt.Fprintf(tw, "  %s\t%d\t%d\t%d ms\t%d/%d\t%d ms\n", route.Route, route.CandidateRequests, route.CandidateServerErrors,
				route.CandidateP95MS, route.StableServerErrors, route.StableRequests, route.StableP95MS)
		}
		_ = tw.Flush()
	}
	if len(resp.Examples) > 0 {
		_, _ = fmt.Fprintln(w, "\nExample requests:")
		for _, req := range resp.Examples {
			_, _ = fmt.Fprintf(w, "  %s  %s  %d  %d ms  gregale debug requests inspect %s %s\n",
				debugClock(req.ReceivedAt), requestLine(req.Method, req.Route), req.Status, req.LatencyMS, slug, debugRequestRef(req))
		}
	}
	if len(resp.Regressions) > 0 {
		_, _ = fmt.Fprintln(w, "\nOpen regressions on this deployment:")
		for _, reg := range resp.Regressions {
			line := fmt.Sprintf("  %s  %s", reg.Route, debugRegressionSignal(reg))
			if dep := reg.SuspectedDependency; dep != nil && dep.Name != "" {
				line += "  suspected: " + dep.Name
			}
			_, _ = fmt.Fprintln(w, line)
		}
	}
}

// debugRegressionSignal renders what regressed: the error rate for an error
// regression, otherwise p95 against the baseline.
func debugRegressionSignal(reg api.DebugRegressionItem) string {
	if reg.Kind == "errors" || reg.Kind == "latency_and_errors" {
		return fmt.Sprintf("errors %.1f%% (was %.1f%%)", reg.ErrorRatePct, reg.ErrorRateBasePct)
	}
	return fmt.Sprintf("p95 %d ms (was %d ms)", reg.P95MS, reg.P95BaseMS)
}

// debugRequestRef is the identifier to pass to `debug requests`: the public
// request ID when known, else the telemetry row ID.
func debugRequestRef(req api.DebugTelemetryRequestItem) string {
	if req.RequestID != "" {
		return req.RequestID
	}
	return req.ID
}

func debugShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// debugClock shows the time of day from an RFC 3339 timestamp, or the raw
// value when it does not parse.
func debugClock(ts string) string {
	if i := strings.IndexByte(ts, 'T'); i >= 0 && len(ts) >= i+9 {
		return ts[i+1 : i+9]
	}
	return ts
}
