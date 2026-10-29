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

type lineageRetryWatchCounts struct {
	Pending           int `json:"pending"`
	Repaired          int `json:"repaired"`
	AlreadyAssociated int `json:"already_associated"`
	Unresolved        int `json:"unresolved"`
	Conflict          int `json:"conflict"`
	Changed           int `json:"changed"`
	Unavailable       int `json:"unavailable"`
	Cancelled         int `json:"cancelled"`
}

type lineageRetryWatchSummary struct {
	Status      string                  `json:"status"`
	Selected    int                     `json:"selected"`
	Processed   int                     `json:"processed"`
	CreatedAt   time.Time               `json:"created_at"`
	RetainUntil time.Time               `json:"retain_until"`
	Counts      lineageRetryWatchCounts `json:"counts"`
}

type lineageRetryWatchRecord struct {
	Event          string                    `json:"event"`
	StopReason     string                    `json:"stop_reason,omitempty"`
	App            string                    `json:"app"`
	SubscriptionID string                    `json:"subscription_id"`
	RequestID      string                    `json:"request_id"`
	Job            *lineageRetryWatchSummary `json:"job,omitempty"`
}

func cmdEventsLineageRetryJobWatch(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-job-watch", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum monitoring duration")
	interval := fs.Duration("poll-interval", 2*time.Second, "delay between reads (1s to 1h)")
	expected := fs.String("expected-created-at", "", "optional original creation timestamp from the reviewed job")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale event subscription-lineage-retry-job-watch APP SUB REQUEST_UUID [--timeout 10m] [--poll-interval 2s] [--expected-created-at TIMESTAMP]", "events")
		return 1
	}
	if *timeout <= 0 || *interval < time.Second || *interval > time.Hour {
		return printErr("Invalid watch options", fmt.Errorf("timeout must be positive and poll interval must be between 1s and 1h"))
	}
	sub, e := uuid.Parse(positional[1])
	id, err := uuid.Parse(positional[2])
	if e != nil || err != nil || sub == uuid.Nil || id == uuid.Nil {
		return printErr("Invalid watch identity", fmt.Errorf("subscription and request IDs must be nonzero UUIDs"))
	}
	var created time.Time
	if *expected != "" {
		created, err = time.Parse(time.RFC3339Nano, *expected)
		if err != nil || created.IsZero() {
			return printErr("Invalid creation guard", fmt.Errorf("expected-created-at must be the original RFC3339 creation timestamp"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	return watchLineageRetryJob(ctx, client, positional[0], sub.String(), id.String(), created, *interval)
}

func summarizeLineageRetryWatch(job api.EventExecutionLineageRetryJob) (lineageRetryWatchSummary, error) {
	out := lineageRetryWatchSummary{Status: job.Status, Selected: len(job.Items), Processed: job.Processed, CreatedAt: job.CreatedAt, RetainUntil: job.RetainUntil}
	appID, err := uuid.Parse(job.AppID)
	if err != nil || appID == uuid.Nil {
		return out, fmt.Errorf("invalid job app identity")
	}
	if out.Selected < 1 || out.Selected > api.EventExecutionLineageRetryJobMaxCases || out.Processed < 0 || out.Processed > out.Selected || job.CreatedAt.IsZero() || !job.RetainUntil.After(job.CreatedAt) {
		return out, fmt.Errorf("invalid job progress or retention")
	}
	if job.Status != "pending" && job.Status != "paused" && job.Status != "completed" && job.Status != "cancelled" {
		return out, fmt.Errorf("unsupported job state %q", job.Status)
	}
	for _, item := range job.Items {
		switch item.Status {
		case "pending":
			out.Counts.Pending++
		case "repaired":
			out.Counts.Repaired++
		case "already_associated":
			out.Counts.AlreadyAssociated++
		case "unresolved":
			out.Counts.Unresolved++
		case "conflict":
			out.Counts.Conflict++
		case "changed":
			out.Counts.Changed++
		case "unavailable":
			out.Counts.Unavailable++
		case "cancelled":
			out.Counts.Cancelled++
		default:
			return out, fmt.Errorf("unsupported case state %q", item.Status)
		}
	}
	if out.Processed != out.Selected-out.Counts.Pending-out.Counts.Cancelled || (job.Status == "completed" && out.Processed != out.Selected) || (job.Status == "cancelled" && out.Counts.Pending != 0) || ((job.Status == "pending" || job.Status == "paused") && out.Counts.Cancelled != 0) {
		return out, fmt.Errorf("job state and outcome counts disagree")
	}
	return out, nil
}

func watchLineageRetryJob(ctx context.Context, client *api.Client, app, sub, id string, expected time.Time, interval time.Duration) int {
	record := lineageRetryWatchRecord{App: app, SubscriptionID: sub, RequestID: id}
	var pin *api.EventExecutionLineageRetryJob
	for {
		readCtx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
		job, err := client.GetEventExecutionLineageRetryJob(readCtx, app, sub, id)
		cancel()
		if err != nil {
			return stopLineageRetryWatch(ctx, record, err, "read_failed")
		}
		if !sameBindingDeployment(job.RequestID, id) || !sameBindingDeployment(job.SubscriptionID, sub) || job.AppID == "" || (!expected.IsZero() && !job.CreatedAt.Equal(expected)) || (pin != nil && (!sameBindingDeployment(job.AppID, pin.AppID) || !job.CreatedAt.Equal(pin.CreatedAt) || !job.RetainUntil.Equal(pin.RetainUntil))) {
			return stopLineageRetryWatch(ctx, record, fmt.Errorf("job identity changed; review its original creation timestamp before restarting monitoring"), "job_changed")
		}
		summary, err := summarizeLineageRetryWatch(job)
		if err != nil {
			return stopLineageRetryWatch(ctx, record, err, "invalid_response")
		}
		if pin == nil {
			pin = &job
		}
		changed := record.Job == nil || *record.Job != summary
		record.Job = &summary
		if job.Status == "completed" || job.Status == "cancelled" {
			record.Event = job.Status
			if err := renderLineageRetryWatch(record); err != nil {
				return printErr("Watch output failed", err)
			}
			if job.Status == "cancelled" {
				return 1
			}
			return 0
		}
		if changed {
			record.Event = "progress"
			if err := renderLineageRetryWatch(record); err != nil {
				return printErr("Watch output failed", err)
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return stopLineageRetryWatch(ctx, record, ctx.Err(), "read_failed")
		case <-timer.C:
		}
	}
}

func stopLineageRetryWatch(ctx context.Context, record lineageRetryWatchRecord, err error, reason string) int {
	var problem *api.APIError
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		reason = "interrupted"
		err = ctx.Err()
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		reason = "timeout"
		err = ctx.Err()
	case errors.As(err, &problem) && problem.Problem.Status == 404:
		reason = "unavailable"
	}
	record.Event, record.StopReason = "stopped", reason
	if outputErr := renderLineageRetryWatch(record); outputErr != nil {
		return printErr("Watch output failed", outputErr)
	}
	return printErr("Lineage retry watch stopped; resume using request "+record.RequestID, err)
}

func renderLineageRetryWatch(record lineageRetryWatchRecord) error {
	if jsonOutput {
		return writeNDJSON([]lineageRetryWatchRecord{record})
	}
	if _, err := fmt.Fprintf(osStdout, "Job %s | %s", record.RequestID, record.Event); err != nil {
		return err
	}
	if record.StopReason != "" {
		if _, err := fmt.Fprintf(osStdout, " (%s)", record.StopReason); err != nil {
			return err
		}
	}
	if job := record.Job; job != nil {
		if _, err := fmt.Fprintf(osStdout, " | last observed %s | processed %d/%d | created %s\n", job.Status, job.Processed, job.Selected, job.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		c := job.Counts
		_, err := fmt.Fprintf(osStdout, "  repaired %d | already associated %d | unresolved %d | conflict %d | changed %d | unavailable %d | pending %d | cancelled %d\n", c.Repaired, c.AlreadyAssociated, c.Unresolved, c.Conflict, c.Changed, c.Unavailable, c.Pending, c.Cancelled)
		return err
	}
	_, err := fmt.Fprintln(osStdout)
	return err
}
