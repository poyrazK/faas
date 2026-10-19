// commands_ai_canary.go — `gregale ai routes canary` (ADR-1340): a route's
// model canary.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiRoutesCanaryUsage = "usage: gregale ai routes canary <route-id> [status|promote|stop] | " +
	"--position <2-5> --percent <1-50> [--max-error-increase <points>] [--max-latency-increase <percent>] " +
	"[--min-calls <n>] [--window <minutes>]"

// cmdAIRoutesCanary starts, shows, promotes or stops a route's canary.
func cmdAIRoutesCanary(client *api.Client, args []string) int {
	if len(args) == 0 {
		return printErr("Invalid ai routes canary arguments", errors.New(aiRoutesCanaryUsage))
	}
	routeID, rest := args[0], args[1:]
	if len(rest) <= 1 {
		action := "status"
		if len(rest) == 1 {
			action = rest[0]
		}
		switch action {
		case "status":
			return cmdAIRoutesCanaryStatus(client, routeID)
		case "promote":
			route, err := client.PromoteOutboundAIRouteCanary(context.Background(), routeID)
			if err != nil {
				return printErr("Could not promote the canary", err)
			}
			if jsonOutput {
				return jsonOut(writeJSON(route))
			}
			_, _ = fmt.Fprintf(osStdout, "Canary promoted: %s is now the first target of %s\n", formatAIRouteTarget(route.Targets[0]), route.Name)
			return 0
		case "stop":
			if err := client.DeleteOutboundAIRouteCanary(context.Background(), routeID); err != nil {
				return printErr("Could not stop the canary", err)
			}
			_, _ = fmt.Fprintf(osStdout, "Canary of route %s stopped\n", routeID)
			return 0
		}
	}
	return cmdAIRoutesCanarySet(client, routeID, rest)
}

func cmdAIRoutesCanarySet(client *api.Client, routeID string, args []string) int {
	fs := newFlagSet("ai routes canary", flag.ContinueOnError)
	position := fs.Int("position", 0, "the route target (2 to 5) that receives calls first")
	percent := fs.Int("percent", 0, "share of the route's calls sent to the canary first, 1 to 50")
	errorIncrease := fs.Int("max-error-increase", api.OutboundAIRouteCanaryErrorIncreaseDefault, "roll back when the error rate is this many points worse")
	latencyIncrease := fs.Int("max-latency-increase", 0, "roll back when mean time to first byte is this many percent worse (0: errors only)")
	minCalls := fs.Int("min-calls", api.OutboundAIRouteCanaryMinCallsDefault, "canary calls needed before judging")
	window := fs.Int("window", api.OutboundAIRouteCanaryWindowMinutesDefault, "minutes of calls to judge")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		return printErr("Invalid ai routes canary arguments", errors.New(aiRoutesCanaryUsage))
	}
	req := api.PutOutboundAIRouteCanaryRequest{Target: *position, Percent: *percent, MaxErrorRateIncrease: errorIncrease,
		MinCalls: minCalls, WindowMinutes: window}
	if *latencyIncrease > 0 {
		req.MaxLatencyIncreasePercent = latencyIncrease
	}
	if !req.Valid() {
		return printErr("Invalid canary", errors.New(aiRoutesCanaryUsage))
	}
	canary, err := client.SetOutboundAIRouteCanary(context.Background(), routeID, req)
	if err != nil {
		return printErr("Could not start the canary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(canary))
	}
	_, _ = fmt.Fprintf(osStdout, "Canary started: target %d takes %d%% of route %s first\n", canary.Target, canary.Percent, routeID)
	return 0
}

func cmdAIRoutesCanaryStatus(client *api.Client, routeID string) int {
	status, err := client.GetOutboundAIRouteCanary(context.Background(), routeID)
	if err != nil {
		return printErr("Could not read the canary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	canary := status.Canary
	state := canary.State
	if canary.Reason != nil {
		state += " (" + *canary.Reason + ")"
	}
	_, _ = fmt.Fprintf(osStdout, "Canary: target %d, %d%% of calls, %s\n  judged after %d canary calls over %d minutes; rollback past +%d error points",
		canary.Target, canary.Percent, state, canary.MinCalls, canary.WindowMinutes, canary.MaxErrorRateIncrease)
	if canary.MaxLatencyIncreasePercent != nil {
		_, _ = fmt.Fprintf(osStdout, " or +%d%% time to first byte", *canary.MaxLatencyIncreasePercent)
	}
	_, _ = fmt.Fprintln(osStdout)
	for _, row := range []struct {
		label string
		stats api.OutboundAIRouteTargetStats
	}{{"baseline", status.Baseline}, {"canary", status.CanaryStats}} {
		ttfb := "-"
		if row.stats.MeanTTFBMS != nil {
			ttfb = fmt.Sprintf("%d ms", *row.stats.MeanTTFBMS)
		}
		_, _ = fmt.Fprintf(osStdout, "  %-8s target %d: %d calls, %d errors (%.1f%%), mean TTFB %s, cost %s\n", row.label, row.stats.Target,
			row.stats.Calls, row.stats.Errors, 100*row.stats.ErrorRate, ttfb, financialMoney(row.stats.CostMillicents))
	}
	return 0
}
