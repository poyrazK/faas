// Package main — builder-VM network preflight and build exit classification.
//
// This file is build-tag-free so the pure helpers are unit-testable on any
// host; main_linux.go calls them from the builder path.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

// builderHTTPSPreflightBudget bounds one HTTPS reachability check from a
// builder VM. A fresh Firecracker guest's first routed TLS connection can take
// several seconds when the node's network path has just come up. The Node
// toolchain check was given 15 s for that reason, but its http.Client carried
// a fixed 5 s Timeout that cut the budget; after every production rollout the
// first Node build on each node failed in ~10 s as "timeout: build exited 124".
const builderHTTPSPreflightBudget = 15 * time.Second

// builderNetworkError marks a failed builder network preflight. It is an
// infrastructure failure of the builder's network path, not a build that ran
// out of time, so it must not be recorded as exit 124 / FailureTimeout.
type builderNetworkError struct{ err error }

func (e builderNetworkError) Error() string { return e.err.Error() }
func (e builderNetworkError) Unwrap() error { return e.err }

// builderHTTPSPreflight GETs url once within builderHTTPSPreflightBudget and
// reports an error unless healthy accepts the response status. The request is
// bounded by its context only; there is no shorter client timeout.
func builderHTTPSPreflight(parent context.Context, name, url string, healthy func(status int) bool) error {
	ctx, cancel := context.WithTimeout(parent, builderHTTPSPreflightBudget)
	defer cancel()
	err := func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		if !healthy(resp.StatusCode) {
			return fmt.Errorf("status %s", resp.Status)
		}
		return nil
	}()
	if err != nil {
		return builderNetworkError{fmt.Errorf("%s HTTPS preflight: %w", name, err)}
	}
	return nil
}

// buildExitStatus maps a build attempt's error to the exit code and failure
// class guest-init records in build-done.json.
func buildExitStatus(runErr error) (int, string) {
	if runErr == nil {
		return 0, ""
	}
	var network builderNetworkError
	if errors.As(runErr, &network) {
		return 1, "FailureInfra"
	}
	exitCode := 1
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		exitCode = ee.ExitCode()
	} else if errors.Is(runErr, context.DeadlineExceeded) {
		exitCode = 124
	}
	return exitCode, classify(exitCode)
}

// classify maps a build exit code to the failure class builderd records.
func classify(exitCode int) string {
	switch exitCode {
	case 137:
		return "FailureOOM"
	case 124:
		return "FailureTimeout"
	case 0:
		return ""
	default:
		return "FailureUserError"
	}
}
