package main

import (
	"context"
	"errors"
	"flag"
	"time"
)

const (
	bindingRotationWaitTimeoutDefault  = 5 * time.Minute
	bindingRotationPollIntervalDefault = time.Second
)

type bindingRotationWaitOptions struct {
	Wait         bool
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

func parseBindingRotationWaitArgs(args []string, command string, positionalCount int) ([]string, bindingRotationWaitOptions, bool) {
	fs := newFlagSet(command, flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the previous credential to retire")
	waitTimeout := fs.Duration("wait-timeout", bindingRotationWaitTimeoutDefault, "maximum time to wait for rotation")
	pollInterval := fs.Duration("poll-interval", bindingRotationPollIntervalDefault, "status polling interval while waiting")
	flagArgs, positionals := splitArgsForFlags(args, "wait")
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 || len(positionals) != positionalCount ||
		*waitTimeout <= 0 || *pollInterval <= 0 {
		return nil, bindingRotationWaitOptions{}, false
	}
	return positionals, bindingRotationWaitOptions{
		Wait: *wait, WaitTimeout: *waitTimeout, PollInterval: *pollInterval,
	}, true
}

// waitForBindingRotation polls only when requested and returns the newest
// binding state available when rotation completes or the wait expires.
func waitForBindingRotation[T any](ctx context.Context, initial T, options bindingRotationWaitOptions, pending func(T) bool, poll func(context.Context) (T, error)) (T, bool, error) {
	if !options.Wait || !pending(initial) {
		return initial, false, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, options.WaitTimeout)
	defer cancel()

	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()

	current := initial
	for pending(current) {
		select {
		case <-waitCtx.Done():
			return current, true, nil
		case <-ticker.C:
		}

		updated, err := poll(waitCtx)
		if err != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
				return current, true, nil
			}
			return current, false, err
		}
		current = updated
	}
	return current, false, nil
}
