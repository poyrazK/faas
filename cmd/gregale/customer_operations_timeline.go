package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1195: one chronological history of an Operation, optionally with the
// archived log lines of each HTTP attempt's instance window.
type customerOperationTimelineClient interface {
	GetOperationTimeline(context.Context, string, string, int, string) (api.OperationTimelineResponse, error)
	StreamAppArchivedLogs(context.Context, string, api.ArchiveLogSelector) (io.ReadCloser, error)
}

type customerOperationTimelineCommand struct {
	app, id, cursor string
	limit           int
	all, logs       bool
	timeout         time.Duration
}

const customerOperationTimelineDataMax = 160

func parseCustomerOperationTimeline(args []string, asJSON bool) (customerOperationTimelineCommand, error) {
	var c customerOperationTimelineCommand
	fs := newFlagSet("customer-operations timeline", flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "account-owned application")
	fs.DurationVar(&c.timeout, "timeout", 0, "local request deadline")
	fs.IntVar(&c.limit, "limit", api.OperationHistoryPageDefault, "bounded page size")
	fs.StringVar(&c.cursor, "cursor", "", "opaque next cursor")
	fs.BoolVar(&c.all, "all", false, "follow cursors to the end of the timeline")
	fs.BoolVar(&c.logs, "logs", false, "print archived instance log lines inside each HTTP attempt")
	if err := parseInterspersed(fs, args); err != nil {
		return c, err
	}
	if fs.NArg() != 1 || c.app == "" || !validCustomerOperationUUID(fs.Arg(0)) || c.timeout < 0 {
		return c, fmt.Errorf("timeline requires an operation UUID and --app")
	}
	c.id = fs.Arg(0)
	if c.limit < 1 || c.limit > api.OperationHistoryPageMax || len(c.cursor) > api.OperationHistoryCursorMaxBytes {
		return c, fmt.Errorf("invalid timeline page")
	}
	if c.logs && asJSON {
		return c, fmt.Errorf("--logs prints human-readable log lines; read log windows from --json instead")
	}
	return c, nil
}

func cmdCustomerOperationTimeline(args []string) int {
	c, err := parseCustomerOperationTimeline(args, jsonOutput)
	if err != nil {
		return printErr("Invalid timeline command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	err = runCustomerOperationTimeline(ctx, client, c, osStdout, jsonOutput)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if err != nil {
		return printErr("Timeline command failed", err)
	}
	return 0
}

func runCustomerOperationTimeline(ctx context.Context, client customerOperationTimelineClient, c customerOperationTimelineCommand, out io.Writer, asJSON bool) error {
	cursor, expiredShown := c.cursor, false
	for {
		r, err := client.GetOperationTimeline(ctx, c.app, c.id, c.limit, cursor)
		if err != nil {
			return err
		}
		if !sameCustomerOperationUUID(r.OperationID, c.id) || len(r.Entries) > c.limit || len(r.NextCursor) > api.OperationHistoryCursorMaxBytes || (r.NextCursor != "" && r.NextCursor == cursor) {
			return fmt.Errorf("mismatched timeline page")
		}
		if asJSON {
			if err := json.NewEncoder(out).Encode(r); err != nil {
				return err
			}
		} else {
			if r.EventsExpired && !expiredShown {
				expiredShown = true
				if _, err := fmt.Fprintln(out, "Event history expired; attempts and deliveries remain."); err != nil {
					return err
				}
			}
			for _, e := range r.Entries {
				if err := printCustomerOperationTimelineEntry(ctx, client, c, out, e); err != nil {
					return err
				}
			}
		}
		if r.NextCursor == "" {
			return nil
		}
		if !c.all {
			if asJSON {
				return nil
			}
			_, err = fmt.Fprintf(out, "Next cursor: %s\n", r.NextCursor)
			return err
		}
		cursor = r.NextCursor
	}
}

func printCustomerOperationTimelineEntry(ctx context.Context, client customerOperationTimelineClient, c customerOperationTimelineCommand, out io.Writer, e api.OperationTimelineEntry) error {
	stamp := e.At.UTC().Format("2006-01-02T15:04:05.000Z")
	var line string
	switch {
	case e.Kind == api.OperationTimelineKindEvent && e.Event != nil:
		line = fmt.Sprintf("event     #%d %s %s", e.Event.Sequence, e.Event.Type, compactCustomerOperationTimelineData(e.Event.Data))
	case (e.Kind == api.OperationTimelineKindAttempt || e.Kind == api.OperationTimelineKindStep) && e.Attempt != nil:
		line = formatCustomerOperationTimelineAttempt(e.Kind, *e.Attempt)
	case e.Kind == api.OperationTimelineKindDelivery && e.Delivery != nil:
		d := e.Delivery
		line = fmt.Sprintf("delivery  %d/%d %s HTTP %d %s", d.ReplayGeneration, d.AttemptNumber, d.Outcome, d.ResponseCode, d.ErrorCode)
	default:
		return fmt.Errorf("malformed timeline entry %q", e.Kind)
	}
	if _, err := fmt.Fprintf(out, "%s  %s\n", stamp, strings.TrimRight(line, " ")); err != nil {
		return err
	}
	if c.logs && e.Attempt != nil && e.Attempt.Logs != nil {
		return printCustomerOperationAttemptLogs(ctx, client, c.app, out, *e.Attempt.Logs)
	}
	return nil
}

func formatCustomerOperationTimelineAttempt(kind string, a api.OperationTimelineAttempt) string {
	label := "attempt  "
	if kind == api.OperationTimelineKindStep {
		label = "step     "
	}
	parts := []string{label}
	if a.Generation > 0 {
		parts[0] += fmt.Sprintf(" g%d", a.Generation)
	}
	if a.Step != "" {
		parts = append(parts, a.Step)
	}
	parts = append(parts, fmt.Sprintf("#%d %s", a.Attempt, a.Outcome))
	if a.HTTPStatus != nil {
		parts = append(parts, fmt.Sprintf("HTTP %d", *a.HTTPStatus))
	}
	if a.FinishedAt != nil {
		parts = append(parts, a.FinishedAt.Sub(a.StartedAt).Round(time.Millisecond).String())
	}
	if a.Error != "" {
		parts = append(parts, "error: "+truncateCustomerOperationTimeline(a.Error))
	}
	if a.Logs != nil {
		parts = append(parts, fmt.Sprintf("logs: instance %s, %s", a.Logs.InstanceID, strings.Join(a.Logs.ArchiveDates, ",")))
	}
	return strings.Join(parts, " ")
}

func compactCustomerOperationTimelineData(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return ""
	}
	return truncateCustomerOperationTimeline(string(raw))
}

func truncateCustomerOperationTimeline(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > customerOperationTimelineDataMax {
		return string(r[:customerOperationTimelineDataMax]) + "…"
	}
	return s
}

// printCustomerOperationAttemptLogs reads each archive day of the window and
// prints the lines written inside it. A missing day is reported, not fatal:
// archives lag the live ring and follow plan retention.
func printCustomerOperationAttemptLogs(ctx context.Context, client customerOperationTimelineClient, app string, out io.Writer, w api.OperationAttemptLogs) error {
	for _, date := range w.ArchiveDates {
		body, err := client.StreamAppArchivedLogs(ctx, app, api.ArchiveLogSelector{InstanceID: w.InstanceID, Date: date})
		if err != nil {
			if _, werr := fmt.Fprintf(out, "    (logs %s: %v)\n", date, err); werr != nil {
				return werr
			}
			continue
		}
		dec := api.NewDecoder(body)
		dec.SetCloseFn(body.Close)
		reason := ""
		_, streamErr := consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
			if e.Event == "end" {
				reason = appLogsArchiveEndReason(e.Data)
				return true, 0
			}
			if e.Data != "" && customerOperationLogInWindow(e.Data, w) {
				_, _ = fmt.Fprintln(out, "    "+formatRuntimeLogLine(e.Data))
			}
			return false, 0
		})
		_ = dec.Close()
		if err := ctx.Err(); err != nil {
			return err
		}
		if streamErr != nil || reason != "archive_complete" {
			if _, err := fmt.Fprintf(out, "    (logs %s: %s)\n", date, appLogsArchiveMessage(reason)); err != nil {
				return err
			}
		}
	}
	return nil
}

func customerOperationLogInWindow(data string, w api.OperationAttemptLogs) bool {
	var event api.LogEvent
	if json.Unmarshal([]byte(data), &event) != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, event.WrittenAt)
	if err != nil || at.Before(w.Since) {
		return false
	}
	return w.Until == nil || !at.After(*w.Until)
}
