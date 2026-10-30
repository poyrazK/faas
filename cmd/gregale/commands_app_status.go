package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	subStatus = "status"
	subWait   = "wait"

	// exitWaitTimeout is the exit code of a wait that ran out of time: the
	// CLI's retry-later code, as job and preview waits use it
	// (ADR-20261030151810).
	exitWaitTimeout = 3
)

// cmdAppStatus prints an app's conditions (ADR-20261030151810).
func cmdAppStatus(slug string, args []string) int {
	fs := newFlagSet("app status", flag.ContinueOnError)
	environment := fs.String("environment", "", "read this project environment's conditions instead of the default scope")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *environment != "" && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid status options", fmt.Errorf("--environment must be a project environment slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := client.GetAppStatus(ctx, slug, *environment)
	if err != nil {
		return printErr("Could not read conditions", appStatusError(slug, err))
	}
	return renderResourceStatus(status)
}

// cmdAppWait polls an app's conditions until one holds. It exits 0 when the
// condition holds, 1 when it is terminal and does not, 3 on timeout (the
// CLI's retry-later code), and 4 when the app or its conditions are not found.
func cmdAppWait(slug string, args []string) int {
	fs := newFlagSet("app wait", flag.ContinueOnError)
	forFlag := fs.String("for", api.ConditionReady, "condition to wait for: TYPE or TYPE=True|False|Unknown")
	environment := fs.String("environment", "", "wait on this project environment's conditions instead of the default scope")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval (1s to 1h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	want, err := parseConditionFor(api.ResourceKindApp, *forFlag)
	if err != nil {
		return printErr("Invalid wait options", err)
	}
	if *environment != "" && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid wait options", fmt.Errorf("--environment must be a project environment slug"))
	}
	if *timeout <= 0 || *interval < time.Second || *interval > time.Hour {
		return printErr("Invalid wait options", fmt.Errorf("--timeout must be positive and --poll-interval between 1s and 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	read := func(ctx context.Context) (api.ResourceStatus, error) {
		status, err := client.GetAppStatus(ctx, slug, *environment)
		return status, appStatusError(slug, err)
	}
	return waitForCondition(read, want, *timeout, *interval)
}

// conditionWant is a parsed --for: the condition type and the status that
// ends the wait.
type conditionWant struct {
	Type, Status string
}

func (w conditionWant) String() string { return w.Type + "=" + w.Status }

func parseConditionFor(kind, value string) (conditionWant, error) {
	typ, status, hasStatus := strings.Cut(strings.TrimSpace(value), "=")
	if !hasStatus {
		status = api.ConditionTrue
	}
	types := api.ConditionTypes[kind]
	if !slices.Contains(types, typ) {
		return conditionWant{}, fmt.Errorf("--for %q: %s conditions are %s", value, kind, strings.Join(types, ", "))
	}
	if status != api.ConditionTrue && status != api.ConditionFalse && status != api.ConditionUnknown {
		return conditionWant{}, fmt.Errorf("--for %q: the status is True, False or Unknown", value)
	}
	return conditionWant{Type: typ, Status: status}, nil
}

// waitForCondition polls read until the wanted condition holds, turns
// terminal without holding, or the timeout passes. Transient read failures
// are retried; a client error (a missing resource, a capability that is off)
// ends the wait.
func waitForCondition(read func(context.Context) (api.ResourceStatus, error), want conditionWant, timeout, interval time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	var last *api.ResourceStatus
	previous := ""
	for ctx.Err() == nil {
		readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
		status, err := read(readCtx)
		readCancel()
		if ctx.Err() != nil {
			break
		}
		progress := "Conditions unavailable; retrying."
		if err != nil {
			var ec *exitErr
			if inspectWatchPermanentError(err) || errors.As(err, &ec) {
				return printErr("Wait failed", err)
			}
		} else {
			last = &status
			c, ok := findCondition(status, want.Type)
			switch {
			case !ok:
				return printErr("Wait failed", fmt.Errorf("%s %s reports no %s condition", status.Kind, status.Name, want.Type))
			case c.Status == want.Status:
				return renderResourceStatus(status)
			case c.Terminal:
				if jsonOutput {
					_ = writeJSON(status)
				}
				return printErr("Condition will not be met", fmt.Errorf("%s %s is %s=%s (%s): %s; it will not change without a new request", status.Kind, status.Name, c.Type, c.Status, c.Reason, c.Message))
			}
			progress = fmt.Sprintf("%s=%s (%s): %s", c.Type, c.Status, c.Reason, c.Message)
		}
		if !jsonOutput && progress != previous {
			if _, err := fmt.Fprintln(osStderr, progress); err != nil {
				return printErr("Could not write wait progress", err)
			}
		}
		previous = progress
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return printErr("Wait canceled", ctx.Err())
	}
	if jsonOutput && last != nil {
		_ = writeJSON(*last)
	}
	_ = printErr("Wait timed out", fmt.Errorf("%s did not hold within %s", want, timeout))
	return exitWaitTimeout
}

func findCondition(status api.ResourceStatus, typ string) (api.Condition, bool) {
	for _, c := range status.Conditions {
		if c.Type == typ {
			return c, true
		}
	}
	return api.Condition{}, false
}

// appStatusError explains a 404, keeping the CLI's not-found exit code: the
// app may be missing, or conditions are not available to the account or the
// server yet.
func appStatusError(slug string, err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
		return &exitErr{code: exitCodeForStatus(http.StatusNotFound), msg: fmt.Sprintf("no conditions for app %s: the app does not exist, or conditions are not available to this account yet; `gregale app health %s --wait` waits for health instead", slug, slug)}
	}
	return err
}

func renderResourceStatus(status api.ResourceStatus) int {
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "%s %s, observed %s\n", status.Kind, status.Name, status.ObservedAt)
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	for _, c := range status.Conditions {
		message := c.Message
		if c.Terminal {
			message += " (terminal)"
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.Type, c.Status, c.Reason, message)
	}
	_ = tw.Flush()
	if status.Kind == api.ResourceKindApp {
		_, _ = fmt.Fprintf(osStdout, "Evidence: gregale app health %s\n", status.Name)
	}
	return 0
}
