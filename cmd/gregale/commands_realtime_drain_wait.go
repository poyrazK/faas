package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const realtimeDrainWaitDefaultTimeout = 5 * time.Minute

// realtimeDrainPollInterval is a variable so command tests can exercise a
// transition without waiting for the normal operator-facing cadence.
var realtimeDrainPollInterval = time.Second

func cmdRealtimeDrainStatus(args []string) int {
	args = normalizeRealtimeDrainStatusArgs(args)
	fs := newFlagSet("realtime drain-status", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the drain to reach a terminal state")
	timeout := fs.Duration("timeout", realtimeDrainWaitDefaultTimeout, "maximum time to wait with --wait")
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" || strings.TrimSpace(fs.Arg(2)) == "" {
		PrintUsage(osStderr, "usage: gregale realtime drain-status APP_SLUG ENDPOINT_ID OPERATION_ID [--wait] [--timeout DURATION]", "realtime")
		return 1
	}
	if *timeout <= 0 {
		return printErr("Invalid wait timeout", fmt.Errorf("must be positive"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.GetManagedRealtimeDrainOperation(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2))
	if err != nil {
		return printErr("Could not read realtime drain status", err)
	}
	timedOut := false
	if *wait && response.Status == "running" {
		response, timedOut, err = waitForManagedRealtimeDrain(context.Background(), client, fs.Arg(0), fs.Arg(1), fs.Arg(2), *timeout, response)
		if err != nil {
			return printErr("Could not read realtime drain status", err)
		}
	}
	if jsonOutput {
		code := jsonOut(writeJSON(response))
		if code != 0 {
			return code
		}
		if timedOut {
			printRealtimeDrainTimeout(osStderr, fs.Arg(0), fs.Arg(1), response.OperationID, *timeout)
			return 3
		}
		return 0
	}
	renderRealtimeDrainStatus(response)
	if timedOut {
		printRealtimeDrainTimeout(osStderr, fs.Arg(0), fs.Arg(1), response.OperationID, *timeout)
		return 3
	}
	return 0
}

func waitForManagedRealtimeDrain(ctx context.Context, client *Client, appSlug, endpointID, operationID string, timeout time.Duration, initial api.ManagedRealtimeDrainResponse) (api.ManagedRealtimeDrainResponse, bool, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	state := initial
	ticker := time.NewTicker(realtimeDrainPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return state, true, nil
			}
			return state, false, waitCtx.Err()
		case <-ticker.C:
			current, err := client.GetManagedRealtimeDrainOperation(waitCtx, appSlug, endpointID, operationID)
			if err != nil {
				if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
					return state, true, nil
				}
				return state, false, err
			}
			state = current
			if state.Status != "running" {
				return state, false, nil
			}
		}
	}
}

func renderRealtimeDrainStatus(response api.ManagedRealtimeDrainResponse) {
	pending := 0
	for _, result := range response.Results {
		if result.Status == "pending" {
			pending++
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Realtime drain %s: %s\n", response.OperationID, response.Status)
	_, _ = fmt.Fprintf(osStdout, "  matched: %d; closed: %d; gone: %d; failed: %d", response.Matched, response.Closed, response.Gone, response.Failed)
	if pending > 0 {
		_, _ = fmt.Fprintf(osStdout, "; pending: %d", pending)
	}
	_, _ = fmt.Fprintln(osStdout)
}

func printRealtimeDrainTimeout(w io.Writer, appSlug, endpointID, operationID string, timeout time.Duration) {
	PrintWarn(w, "Realtime drain %s did not finish after %s; the server continues processing.", operationID, timeout)
	PrintProgress(w, "next: gregale realtime drain-status %s %s %s", appSlug, endpointID, operationID)
}

func normalizeRealtimeDrainStatusArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args, map[string]bool{"--timeout": true}, map[string]bool{"--wait": true})
}
