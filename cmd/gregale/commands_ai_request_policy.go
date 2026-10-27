// commands_ai_request_policy.go — `gregale ai request-policy`: an AI
// integration's rate, concurrency and timeout (ADR 20261027132936).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiRequestPolicyUsage = "usage: gregale ai request-policy <integration-id> [--rate N] [--burst N] [--max-in-flight N] [--timeout SECONDS]"

func cmdAIRequestPolicy(args []string) int {
	fs := newFlagSet("ai request-policy", flag.ContinueOnError)
	rate := fs.String("rate", "", "calls per second")
	burst := fs.String("burst", "", "calls above the rate allowed at once")
	maxInFlight := fs.String("max-in-flight", "", "calls in flight at once")
	timeout := fs.String("timeout", "", "seconds to wait for a response's headers")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai request-policy arguments", errors.New(aiRequestPolicyUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	offers, err := client.ListOutboundIntegrationOffers(ctx)
	if err != nil {
		return printErr("Could not read the integration", err)
	}
	var policy *api.OutboundRequestPolicy
	for i := range offers.Items {
		if offers.Items[i].ID == fs.Arg(0) {
			policy = &offers.Items[i].RequestPolicy
		}
	}
	if policy == nil {
		return printErr("Integration not found", fmt.Errorf("no outbound integration %s in this account", fs.Arg(0)))
	}
	changed, err := applyAIRequestPolicyFlags(policy, *rate, *burst, *maxInFlight, *timeout)
	if err != nil {
		return printErr("Invalid request policy", err)
	}
	if changed {
		if err := client.SetOutboundIntegrationRequestPolicy(ctx, fs.Arg(0), *policy); err != nil {
			return printErr("Could not update the request policy", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	_, _ = fmt.Fprintf(osStdout, "Request policy for %s: %s calls/s, burst %d, %d in flight, %s s to the response headers\n",
		fs.Arg(0), strconv.FormatFloat(policy.RatePerSecond, 'f', -1, 64), policy.Burst, policy.MaxInFlight,
		strconv.FormatFloat(float64(policy.RequestTimeoutMS)/1000, 'f', -1, 64))
	return 0
}

// applyAIRequestPolicyFlags sets the flags given and keeps the rest.
func applyAIRequestPolicyFlags(policy *api.OutboundRequestPolicy, rate, burst, maxInFlight, timeout string) (bool, error) {
	changed := false
	if rate != "" {
		value, err := strconv.ParseFloat(rate, 64)
		if err != nil || value <= 0 {
			return false, fmt.Errorf("--rate %q is not a positive number", rate)
		}
		policy.RatePerSecond, changed = value, true
	}
	for _, field := range []struct {
		name  string
		value string
		dst   *int
		scale int
	}{
		{"--burst", burst, &policy.Burst, 1},
		{"--max-in-flight", maxInFlight, &policy.MaxInFlight, 1},
		{"--timeout", timeout, &policy.RequestTimeoutMS, 1000},
	} {
		if field.value == "" {
			continue
		}
		value, err := strconv.Atoi(field.value)
		if err != nil || value < 1 || value > 1_000_000 {
			return false, fmt.Errorf("%s %q is not a positive whole number", field.name, field.value)
		}
		*field.dst, changed = value*field.scale, true
	}
	return changed, nil
}
