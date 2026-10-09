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

const appRestartUsage = "usage: gregale app <slug> restart [--fresh [--wait]] [--timeout 10m] [--poll-interval 2s] [--json]"
const appRestartStatusUsage = "usage: gregale app <slug> restart status --wake-id UUID [--wait] [--timeout 10m] [--poll-interval 2s] [--json]"

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
	fs.BoolVar(&out.wait, "wait", false, "wait for restart processing, separately from application health")
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
	return "gregale app " + slug + " restart status --wake-id " + wakeID + " --wait"
}

func followAppRestart(ctx, signalCtx context.Context, client restartStatusClient, slug string, options appRestartOptions, accepted bool) int {
	var update func(api.RuntimeConfigRestartStatusResponse)
	if options.wait && !jsonOutput {
		update = func(row api.RuntimeConfigRestartStatusResponse) {
			_, _ = fmt.Fprintf(osStderr, "Restart %s: %s · %s\n", row.WakeID, row.Status, row.ProgressMessage())
		}
	}
	last, err := pollAppRestartStatus(ctx, client, slug, options.wakeID, options.wait, options.interval, update)
	if last.WakeID != "" {
		if code := outputAppRestartStatus(slug, last); code != 0 {
			return code
		}
	} else if accepted && jsonOutput {
		if code := jsonOut(writeJSON(api.AppRestartResponse{WakeID: options.wakeID})); code != 0 {
			return code
		}
	}
	if err != nil && options.wait && last.Status != "failed" {
		_, _ = fmt.Fprintf(osStderr, "Resume following this request: %s\n", restartStatusCommand(slug, options.wakeID))
	}
	return checkedRollbackCLIError(signalCtx, "Restart status could not establish completion", err)
}

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

func outputAppRestartStatus(slug string, row api.RuntimeConfigRestartStatusResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	next := row.ProgressNextStep()
	if row.Status == "completed" {
		next = "Check current application health: gregale inspect " + slug
	}
	_, _ = fmt.Fprintf(osStdout, "Restart %s: %s\n  %s\n  attempts: %d\n  next: %s\n", row.WakeID, row.Status, row.ProgressMessage(), row.Attempts, next)
	return 0
}
