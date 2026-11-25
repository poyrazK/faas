package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const appRestartUsage = "usage: gregale app restart <slug> [--fresh [--wait]] [--timeout 10m] [--poll-interval 2s] [--json]"
const appRestartStatusUsage = "usage: gregale app restart status <slug> --wake-id UUID [--wait] [--timeout 10m] [--poll-interval 2s] [--json]"

type restartStatusClient interface {
	GetRuntimeConfigRestartStatus(context.Context, string, string) (api.RuntimeConfigRestartStatusResponse, error)
}

type appRestartClient interface {
	restartStatusClient
	RestartApp(context.Context, string) (api.AppRestartResponse, error)
	RestartAppFresh(context.Context, string) (api.AppRestartResponse, error)
}

type appRestartOptions struct {
	status, fresh, wait bool
	wakeID              string
	timeout, interval   time.Duration
}

func parseAppRestartOptions(args []string) (appRestartOptions, error) {
	var out appRestartOptions
	if len(args) > 0 && args[0] == "status" {
		out.status, args = true, args[1:]
	}
	fs := newFlagSet("app restart", flag.ContinueOnError)
	if out.status {
		fs.StringVar(&out.wakeID, "wake-id", "", "accepted fresh restart UUID")
	} else {
		fs.BoolVar(&out.fresh, "fresh", false, "cold-boot with current runtime configuration")
	}
	fs.BoolVar(&out.wait, "wait", false, "wait until the restart is processed and the app's replicas pass their readiness checks")
	fs.DurationVar(&out.timeout, "timeout", api.AppRestartWaitTimeoutDefault, "client deadline")
	fs.DurationVar(&out.interval, "poll-interval", api.AppRestartPollIntervalDefault, "status polling interval")
	if err := fs.Parse(args); err != nil {
		return out, err
	}
	if fs.NArg() != 0 || out.timeout <= 0 || out.interval <= 0 {
		return out, errors.New("unexpected arguments or non-positive timeout/poll interval")
	}
	if out.status {
		id, err := uuid.Parse(out.wakeID)
		if err != nil || id == uuid.Nil {
			return out, errors.New("--wake-id must identify an accepted fresh restart")
		}
		out.wakeID = id.String()
	} else if out.wait && !out.fresh {
		return out, errors.New("durable waiting requires --fresh; snapshot restarts do not have durable restart status")
	}
	return out, nil
}

func cmdAppRestart(slug string, args []string) int {
	usage := appRestartUsage
	if len(args) > 0 && args[0] == "status" {
		usage = appRestartStatusUsage
	}
	if hasHelpFlag(args) {
		PrintUsage(osStdout, usage, "apps")
		return 0
	}
	options, err := parseAppRestartOptions(args)
	if err != nil || !validCLISlug(slug) {
		if err == nil {
			err = errors.New("invalid app slug")
		}
		PrintUsage(osStderr, usage, "apps")
		return printErr("Invalid restart arguments", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, options.timeout)
	defer cancel()
	if options.status {
		return followAppRestart(ctx, signalCtx, client, slug, options, false)
	}
	return submitAppRestart(ctx, signalCtx, client, slug, options)
}

func submitAppRestart(ctx, signalCtx context.Context, client appRestartClient, slug string, options appRestartOptions) int {
	var receipt api.AppRestartResponse
	var err error
	if options.fresh {
		receipt, err = client.RestartAppFresh(ctx, slug)
	} else {
		receipt, err = client.RestartApp(ctx, slug)
	}
	if err != nil {
		return checkedRollbackCLIError(signalCtx, "Restart request failed", err)
	}
	if options.fresh {
		id, err := uuid.Parse(receipt.WakeID)
		if err != nil || id == uuid.Nil {
			if jsonOutput {
				_ = writeJSON(receipt)
			}
			return printErr("Restart accepted; status unavailable", errors.New("server did not return a valid restart ID; do not submit another restart to check progress"))
		}
		options.wakeID = id.String()
	}
	if !options.wait {
		if jsonOutput {
			return jsonOut(writeJSON(receipt))
		}
		PrintOK(osStdout, "Restart requested (wake_id=%s)", receipt.WakeID)
		if options.fresh {
			_, _ = fmt.Fprintln(osStdout, restartStatusCommand(slug, receipt.WakeID))
		}
		return 0
	}
	_, _ = fmt.Fprintf(osStderr, "Restart accepted (wake_id=%s). %s\n", options.wakeID, restartStatusCommand(slug, options.wakeID))
	return followAppRestart(ctx, signalCtx, client, slug, options, true)
}

func restartStatusCommand(slug, wakeID string) string {
	return "gregale app restart status " + slug + " --wake-id " + wakeID + " --wait"
}

func followAppRestart(ctx, signalCtx context.Context, client restartStatusClient, slug string, options appRestartOptions, accepted bool) int {
	var update func(api.RuntimeConfigRestartStatusResponse)
	if options.wait && !jsonOutput {
		update = func(row api.RuntimeConfigRestartStatusResponse) {
			_, _ = fmt.Fprintf(osStderr, "Restart %s: %s · %s\n", row.WakeID, row.Status, row.ProgressMessage())
		}
	}
	last, err := pollAppRestartStatus(ctx, client, slug, options.wakeID, options.wait, options.interval, update)
	ready := false
	if err == nil && options.wait && last.Status == "completed" {
		// --wait means the app can serve again, not only that the restart
		// was processed (microservices-demo feedback, 2026-11-16).
		if health, ok := client.(appReadinessClient); ok {
			if readyErr := waitAppReadiness(ctx, health, slug, options.interval); readyErr != nil {
				var parked errAppParkedWhileWaiting
				if errors.As(readyErr, &parked) {
					return printErr("Restart completed; app parked", readyErr)
				}
				if signalCtx.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return printErr("Restart completed; app not ready yet", fmt.Errorf("stopped waiting after %s; check: gregale app health %s", options.timeout, slug))
				}
				return printErr("Restart completed; readiness unknown", readyErr)
			}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "App %s is ready: its replicas passed their readiness checks.\n", slug)
			}
			ready = true
		}
	}
	if last.WakeID != "" {
		if code := outputAppRestartStatus(slug, last, ready); code != 0 {
			return code
		}
	} else if accepted && jsonOutput {
		if code := jsonOut(writeJSON(api.AppRestartResponse{WakeID: options.wakeID})); code != 0 {
			return code
		}
	}
	if errors.Is(err, errRestartStartupCrash) {
		return printErr("Restart is not taking effect", err)
	}
	if err != nil && options.wait && last.Status != "failed" {
		_, _ = fmt.Fprintf(osStderr, "Resume following this request: %s\n", restartStatusCommand(slug, options.wakeID))
	}
	// --timeout ending the wait is not a network failure: the restart is
	// still being processed server-side. Say so instead of "Could not reach".
	if err != nil && signalCtx.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status := "no status was read"
		if last.WakeID != "" {
			status = "last status: " + last.Status + " · " + last.ProgressMessage()
		}
		return printErr("Restart still processing", fmt.Errorf("stopped waiting after %s (%s); the restart continues", options.timeout, status))
	}
	return checkedRollbackCLIError(signalCtx, "Restart status could not establish completion", err)
}

// errRestartStartupCrash ends a restart wait whose replacement instance
// crashed during startup. The server retries up to its outbox limit, but a
// crash under the config the restart applies repeats on every attempt, so
// `app restart --fresh --wait` printed "running"/"retrying" until its 10 min
// timeout (prod hunt r52, rc.381).
var errRestartStartupCrash = errors.New("the new instance crashed during startup with the current config, which retrying will not fix; the running instance keeps serving")

func pollAppRestartStatus(ctx context.Context, client restartStatusClient, slug, wakeID string, wait bool, interval time.Duration, update func(api.RuntimeConfigRestartStatusResponse)) (api.RuntimeConfigRestartStatusResponse, error) {
	var last api.RuntimeConfigRestartStatusResponse
	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		row, err := client.GetRuntimeConfigRestartStatus(ctx, slug, wakeID)
		if err != nil {
			return last, err
		}
		if err := validateAppRestartStatus(row, wakeID); err != nil {
			return last, err
		}
		if update != nil && (row.Status != last.Status || row.Attempts != last.Attempts || row.FailureReason != last.FailureReason) {
			update(row)
		}
		last = row
		if row.Status == "failed" {
			return last, errors.New(row.ProgressMessage())
		}
		if row.Status == "completed" || !wait {
			return last, nil
		}
		if row.Status == "retrying" && row.FailureReason == api.CodeAppStartupCrash {
			return last, fmt.Errorf("%w. %s", errRestartStartupCrash, row.ProgressNextStep())
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func validateAppRestartStatus(row api.RuntimeConfigRestartStatusResponse, wakeID string) error {
	if row.WakeID != wakeID || row.Attempts < 0 || row.RequestedAt.IsZero() {
		return errors.New("server returned an invalid or different restart receipt")
	}
	switch row.Status {
	case "queued", "running", "retrying", "failed":
		return nil
	case "completed":
		if row.CompletedAt != nil && !row.CompletedAt.IsZero() {
			return nil
		}
		return errors.New("server did not return a restart completion timestamp")
	default:
		return errors.New("server returned an unknown restart state")
	}
}

func outputAppRestartStatus(slug string, row api.RuntimeConfigRestartStatusResponse, ready bool) int {
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	message, next := row.ProgressMessage(), row.ProgressNextStep()
	if row.Status == "completed" {
		next = "Restart processing completed; application health still needs verification."
		if ready {
			// --wait checked readiness, so the server's "health has not been
			// verified" line would contradict the result (prod check 2026-11-16).
			message = "Restart processing is complete and the app's replicas passed their readiness checks."
			next = "The app is ready to serve."
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Restart %s: %s\n  %s\n  attempts: %d\n  next: %s\n", row.WakeID, row.Status, message, row.Attempts, next)
	if !row.RequestedAt.IsZero() {
		_, _ = fmt.Fprintf(osStdout, "  Requested: %s\n", row.RequestedAt.UTC().Format(time.RFC3339))
	}
	if row.CompletedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "  Processing finished: %s\n", row.CompletedAt.UTC().Format(time.RFC3339))
	}
	command := recoveryCLI() + " app restart status " + recoveryArgument(slug) + " --wake-id " + recoveryArgument(row.WakeID) + " --wait"
	switch row.Status {
	case "completed":
		command = recoveryCLI() + " app health " + recoveryArgument(slug)
	case "failed":
		command = recoveryCLI() + " app health " + recoveryArgument(slug) + " --explain"
	}
	_, _ = fmt.Fprintf(osStdout, "  Next command (POSIX shells):\n    %s\n", command)
	return 0
}

// appReadinessClient reads app health; the API client satisfies it.
type appReadinessClient interface {
	GetAppHealth(ctx context.Context, slug string) (api.AppHealthResponse, error)
}

// appStateReader reads the app itself; the API client satisfies it.
type appStateReader interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
}

// errAppParkedWhileWaiting ends a readiness wait: the app was parked for a
// failure, so no replica will become ready until someone acts.
type errAppParkedWhileWaiting struct{ app api.AppResponse }

func (e errAppParkedWhileWaiting) Error() string {
	return fmt.Sprintf("%s is %s, so it will not become ready on its own; see why: gregale logs %s --since 15m",
		e.app.Slug, appParkedLabel(e.app), e.app.Slug)
}

// failureParked reports a park no wait outlasts: a crash loop or a security
// scan, not an idle park.
func failureParked(app api.AppResponse) bool {
	if app.Status != appStatusParked || app.ParkedDeployment == nil {
		return false
	}
	switch app.ParkedDeployment.ParkedReason {
	case "liveness_exhausted", "security_scan_regressed":
		return true
	}
	return false
}

// waitAppReadiness polls app health until its readiness check passes (or the
// app intentionally runs no replicas). ctx bounds the wait. It prints the
// readiness detail as it changes, and stops when the app is parked for a
// failure: a crash-looping worker held `app restart --project --wait` for the
// whole timeout with no output (agent eval 2026-11-23, voting-app P9).
func waitAppReadiness(ctx context.Context, client appReadinessClient, slug string, interval time.Duration) error {
	lastDetail := ""
	for {
		health, err := client.GetAppHealth(ctx, slug)
		if err == nil {
			for _, check := range health.Checks {
				if check.Code != "readiness" {
					continue
				}
				if check.Status == "pass" || check.Status == "not_applicable" {
					return nil
				}
				if check.Detail != lastDetail && !jsonOutput {
					_, _ = fmt.Fprintf(osStderr, "Waiting for %s to become ready: %s\n", slug, check.Detail)
					lastDetail = check.Detail
				}
			}
			if reader, ok := client.(appStateReader); ok {
				if app, appErr := reader.GetApp(ctx, slug); appErr == nil && failureParked(app) {
					return errAppParkedWhileWaiting{app: app}
				}
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
