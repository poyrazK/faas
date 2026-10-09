package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSubscriptionRetry(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes", "jitter")
	fs := newFlagSet("events subscription-retry-"+action, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm changing retry policy for future events")
	policy := api.DefaultEventRoutingRetryPolicy()
	var attempts *int
	var initial, maximum, duration, age *time.Duration
	var jitter *bool
	if action == "set" {
		age = fs.Duration("max-delivery-age", 0, "wall-clock age since acceptance; 0 disables expiry, maximum 720h")
		attempts = fs.Int("max-attempts", policy.MaxAttempts, "maximum routing attempts including first attempt")
		initial = fs.Duration("initial-backoff", time.Duration(policy.InitialBackoffMS)*time.Millisecond, "initial retry delay")
		maximum = fs.Duration("max-backoff", time.Duration(policy.MaxBackoffMS)*time.Millisecond, "maximum retry delay")
		duration = fs.Duration("max-retry-duration", 0, "attempt-time plus scheduled-delay budget; 0 disables this bound")
		jitter = fs.Bool("jitter", true, "spread retries using deterministic full jitter")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes {
		PrintUsage(os.Stderr, "usage: gregale events subscription-retry-"+action+" <app> <subscription-id> [policy flags] [--yes]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	if attempts != nil {
		if *initial%time.Millisecond != 0 || *maximum%time.Millisecond != 0 || *duration%time.Millisecond != 0 || *age%time.Millisecond != 0 {
			return printErr("Invalid retry policy", fmt.Errorf("durations must use whole milliseconds"))
		}
		policy = api.EventRoutingRetryPolicy{MaxDeliveryAgeMS: age.Milliseconds(), MaxAttempts: *attempts, InitialBackoffMS: initial.Milliseconds(), MaxBackoffMS: maximum.Milliseconds(), MaxRetryDurationMS: duration.Milliseconds(), Jitter: *jitter}
		if err := policy.Validate(); err != nil {
			return printErr("Invalid retry policy", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventRoutingRetryPolicyResponse
	switch action {
	case "set":
		out, err = client.SetEventSubscriptionRetryPolicy(context.Background(), positional[0], positional[1], policy)
	case "reset":
		out, err = client.ResetEventSubscriptionRetryPolicy(context.Background(), positional[0], positional[1])
	default:
		out, err = client.GetEventSubscriptionRetryPolicy(context.Background(), positional[0], positional[1])
	}
	if err != nil {
		return printErr("Retry policy failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | configured %t | attempts %d | initial %s | max backoff %s | duration budget %s (0=unlimited) | jitter %t | delivery age %s (0=unlimited)\n", oneLine(out.SubscriptionID), out.Configured, out.Policy.MaxAttempts, time.Duration(out.Policy.InitialBackoffMS)*time.Millisecond, time.Duration(out.Policy.MaxBackoffMS)*time.Millisecond, time.Duration(out.Policy.MaxRetryDurationMS)*time.Millisecond, out.Policy.Jitter, time.Duration(out.Policy.MaxDeliveryAgeMS)*time.Millisecond)
	return 0
}
