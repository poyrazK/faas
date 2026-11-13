package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func resolveInvokeWaitFlags(fs *flag.FlagSet, async, wait bool, timeout, interval time.Duration) (bool, error) {
	var hasWaitOption, explicitAsync bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "timeout", "interval":
			hasWaitOption = true
		case "async":
			explicitAsync = true
		}
	})
	if wait && explicitAsync && !async {
		return false, errors.New("--wait conflicts with --async=false")
	}
	if hasWaitOption && !wait {
		return false, errors.New("--timeout and --interval require --wait")
	}
	if err := validateInvocationWaitDurations(timeout, interval); err != nil {
		return false, err
	}
	return async || wait, nil
}

func validateInvocationWaitDurations(timeout, interval time.Duration) error {
	if timeout < 0 || interval <= 0 {
		return errors.New("--timeout must be nonnegative and --interval must be positive")
	}
	return nil
}

func waitForAcceptedInvocation(ctx context.Context, client *api.Client, id string, timeout, interval time.Duration) int {
	// Do not turn an invalid receipt into another submission or follow an
	// arbitrary status_url. Poll only the accepted ID through this client.
	if !validInvocationWaitID(id) {
		return printErr("Cannot wait for accepted invocation", errors.New("async response has a missing or invalid invocation ID; inspect 'gregale invocation list' before submitting again"))
	}
	if !jsonOutput {
		PrintProgress(osStderr, "Async invocation %s accepted; waiting for its result…", id)
	}
	// The duration bounds local polling after acceptance, not submission or
	// server execution. Stopping this context never cancels durable work.
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	inv, err := waitForInvocation(ctx, client, id, interval)
	return renderInvocationWaitOutcome(ctx, id, inv, err, true)
}

func validInvocationWaitID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	// Invocation IDs must be a single unescaped URL path segment. Permit
	// opaque unreserved IDs as well as the server's UUIDs.
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r) {
			continue
		}
		return false
	}
	return true
}

func invocationResumeHint(id string) string {
	return fmt.Sprintf("Resume waiting for invocation %s with: %s. Do not submit it again.", id,
		savedNameSuggestionCommand([]string{"invocation", "wait", id}))
}
