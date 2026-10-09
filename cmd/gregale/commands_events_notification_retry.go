package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsRecoveryNotificationRetryPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-notification-retry-preview", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-notification-retry-preview <job-id>", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.PreviewEventRecoveryNotificationRetry(context.Background(), positional[0])
	if err != nil {
		return printErr("Notification retry preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery %s | complete evidence: %t\nKIND\tWEBHOOK\tDELIVERY\tGENERATION\tSTATUS\tELIGIBLE\tREASON\n", oneLine(out.JobID), out.CountsComplete)
	for _, r := range out.Receivers {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%s\t%t\t%s\n", oneLine(r.Kind), oneLine(r.WebhookID), oneLine(r.DeliveryID), r.ReplayGeneration, oneLine(r.Status), r.Eligible, oneLine(r.Reason))
	}
	return 0
}
func readRecoveryNotificationRetryRequest(path string) (api.EventRecoveryNotificationRetryRequest, error) {
	var req api.EventRecoveryNotificationRetryRequest
	f, err := os.Open(path)
	if err != nil {
		return req, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.EventRecoveryNotificationRetryBodyMaxBytes+1))
	if err != nil {
		return req, err
	}
	if len(raw) > api.EventRecoveryNotificationRetryBodyMaxBytes {
		return req, fmt.Errorf("request file exceeds %d bytes", api.EventRecoveryNotificationRetryBodyMaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return req, fmt.Errorf("request file must contain one JSON object")
	}
	return req, req.Validate()
}
func cmdEventsRecoveryNotificationRetry(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-notification-retry", flag.ContinueOnError)
	path := fs.String("request-file", "", "JSON request with stable request_id and explicit targets")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || *path == "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-notification-retry <job-id> --request-file PATH", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	req, err := readRecoveryNotificationRetryRequest(*path)
	if err != nil {
		return printErr("Invalid notification retry request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.RetryEventRecoveryNotifications(context.Background(), positional[0], req)
	if err != nil {
		return printErr("Notification retry failed; preserve the request ID when retrying", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery %s | request: %s | decided: %s\n", oneLine(out.JobID), oneLine(out.RequestID), out.DecidedAt.Format("2006-01-02T15:04:05Z07:00"))
	for _, r := range out.Results {
		_, _ = fmt.Fprintf(osStdout, "%s | %s | %s | %s | %s\n", oneLine(r.Target.Kind), oneLine(r.Target.WebhookID), oneLine(r.Target.DeliveryID), oneLine(r.State), oneLine(r.Reason))
	}
	return 0
}

func cmdEventsRecoveryNotificationRetryHistory(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-notification-retry-history", flag.ContinueOnError)
	requestID := fs.String("request-id", "", "show one saved retry request in detail")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-notification-retry-history <job-id> [--request-id UUID]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	if *requestID != "" {
		if _, err := uuid.Parse(*requestID); err != nil {
			return printErr("Invalid request ID", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if jsonOutput {
		if *requestID != "" {
			out, err := client.GetEventRecoveryNotificationRetryDecision(context.Background(), positional[0], *requestID)
			if err != nil {
				return printErr("Notification retry history failed", err)
			}
			return jsonOut(writeJSON(out))
		}
		out, err := client.ListEventRecoveryNotificationRetryHistory(context.Background(), positional[0])
		if err != nil {
			return printErr("Notification retry history failed", err)
		}
		return jsonOut(writeJSON(out))
	}
	if *requestID != "" {
		out, err := client.GetEventRecoveryNotificationRetryDecision(context.Background(), positional[0], *requestID)
		if err != nil {
			return printErr("Notification retry history failed", err)
		}
		_, _ = fmt.Fprintf(osStdout, "Recovery %s | request: %s | decided: %s | current status observed: %s\n", oneLine(out.JobID), oneLine(out.RequestID), out.DecidedAt.Format(time.RFC3339), out.CurrentStatusObservedAt.Format(time.RFC3339))
		_, _ = fmt.Fprintln(osStdout, "KIND\tWEBHOOK\tDELIVERY\tORIGINAL\tREASON\tCURRENT\tGENERATION")
		for _, d := range out.Decisions {
			generation := "unavailable"
			if d.CurrentReplayGeneration != nil {
				generation = fmt.Sprint(*d.CurrentReplayGeneration)
			}
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(d.Target.Kind), oneLine(d.Target.WebhookID), oneLine(d.Target.DeliveryID), oneLine(d.State), oneLine(d.Reason), oneLine(d.CurrentDeliveryStatus), generation)
		}
		return 0
	}
	out, err := client.ListEventRecoveryNotificationRetryHistory(context.Background(), positional[0])
	if err != nil {
		return printErr("Notification retry history failed", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery %s | app: %s | observed: %s\n", oneLine(out.JobID), oneLine(out.AppID), out.ObservedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintln(osStdout, "REQUEST\tDECIDED\tTARGETS\tQUEUED\tSKIPPED")
	for _, d := range out.Decisions {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%d\t%d\t%d\n", oneLine(d.RequestID), d.DecidedAt.Format(time.RFC3339), d.TargetCount, d.QueuedCount, d.SkippedCount)
	}
	return 0
}
