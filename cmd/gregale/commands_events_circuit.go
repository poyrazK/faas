package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
)

func cmdEventsSubscriptionCircuit(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events subscription-circuit-"+action, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm circuit breaker change")
	policy := api.DefaultEventCircuitBreakerPolicy()
	if action == "set" {
		fs.Float64Var(&policy.FailureThresholdPct, "failure-threshold-pct", policy.FailureThresholdPct, "failure percentage that opens the circuit")
		fs.Int64Var(&policy.MinSamples, "min-samples", policy.MinSamples, "minimum routing outcomes in the window")
		fs.Int64Var(&policy.WindowSeconds, "window-seconds", policy.WindowSeconds, "failure observation window, 1..3600 seconds")
		fs.Int64Var(&policy.CooldownSeconds, "cooldown-seconds", policy.CooldownSeconds, "wait before probing, 1..3600 seconds")
		fs.IntVar(&policy.ProbeSuccesses, "probe-successes", policy.ProbeSuccesses, "successful sequential probes required, 1..20")
		fs.IntVar(&policy.RecoveryMaxRate, "recovery-max-rate", policy.RecoveryMaxRate, "maximum recovery routing rate, 1..100 per second")
		fs.Int64Var(&policy.RecoverySeconds, "recovery-seconds", policy.RecoverySeconds, "paced recovery duration, 1..3600 seconds")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes {
		PrintUsage(os.Stderr, "usage: gregale events subscription-circuit-"+action+" <app> <subscription-id> [policy flags] [--yes]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	if err := policy.Validate(); err != nil {
		return printErr("Invalid circuit policy", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventCircuitBreakerResponse
	switch action {
	case "set":
		out, err = client.SetEventCircuitBreaker(context.Background(), positional[0], positional[1], policy)
	case "disable":
		out, err = client.DisableEventCircuitBreaker(context.Background(), positional[0], positional[1])
	case "reset":
		out, err = client.ResetEventCircuitBreaker(context.Background(), positional[0], positional[1])
	default:
		out, err = client.GetEventCircuitBreaker(context.Background(), positional[0], positional[1])
	}
	if err != nil {
		return printErr("Circuit breaker request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | enabled %t | state %s | reason %s | probe in flight %t | recovery rate %d/s | manually paused %t | history incomplete %t\n", oneLine(out.SubscriptionID), out.Enabled, oneLine(out.State), oneLine(out.Reason), out.ProbeInFlight, out.RecoveryRatePerSecond, out.ManualPaused, out.HistoryIncomplete)
	return 0
}
