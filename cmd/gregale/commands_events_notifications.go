package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

func cmdEventsRecoveryNotifications(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events recovery-notifications", flag.ContinueOnError)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events recovery-notifications <job-id>", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid recovery job ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEventRecoveryNotifications(context.Background(), positional[0])
	if err != nil {
		return printErr("Recovery notifications failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery %s | app: %s | observed: %s\n", oneLine(out.JobID), oneLine(out.AppSlug), out.ObservedAt.Format(time.RFC3339))
	for _, notice := range out.Notifications {
		_, _ = fmt.Fprintf(osStdout, "%s | event: %s | capture: %s | acknowledgement: %s | evidence: %s\n", oneLine(notice.Kind), oneLine(notice.Event), oneLine(notice.CaptureStatus), oneLine(notice.AcknowledgementStatus), oneLine(notice.EvidenceSource))
		if notice.SelectedRecipientCount != nil {
			_, _ = fmt.Fprintf(osStdout, "Selected receivers: %d\n", *notice.SelectedRecipientCount)
		}
		if !notice.CountsComplete && notice.CaptureStatus != "pending" && notice.CaptureStatus != "not_applicable" {
			_, _ = fmt.Fprintln(osStdout, "Receiver evidence is incomplete; observed acknowledgements do not prove all receivers acknowledged.")
		}
		_, _ = fmt.Fprintln(osStdout, "WEBHOOK\tDELIVERY\tSTATUS\tATTEMPT\tREPLAY\tHTTP\tNEXT RETRY\tAVAILABLE")
		for _, receiver := range notice.Receivers {
			next := ""
			if receiver.NextAttemptAt != nil {
				next = receiver.NextAttemptAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%t\n", oneLine(receiver.WebhookID), oneLine(receiver.DeliveryID), oneLine(receiver.Status), receiver.Attempt, receiver.ReplayGeneration, receiver.LastResponseCode, next, receiver.ReceiverAvailable)
			if receiver.AttemptsPath != "" {
				_, _ = fmt.Fprintf(osStdout, "  Attempt history: GET %s\n", oneLine(receiver.AttemptsPath))
			}
			if receiver.RetryPath != "" {
				_, _ = fmt.Fprintf(osStdout, "  Retry: gregale webhooks retry --app %s %s %s\n", oneLine(out.AppSlug), oneLine(receiver.WebhookID), oneLine(receiver.DeliveryID))
			}
		}
	}
	return 0
}
