package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type notificationRetryWaitCounts struct {
	Queued    int `json:"queued"`
	Skipped   int `json:"skipped"`
	Pending   int `json:"pending"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Unknown   int `json:"unknown"`
}

type notificationRetryWaitReceipt struct {
	JobID           string                                            `json:"job_id"`
	RequestID       string                                            `json:"request_id"`
	Status          string                                            `json:"status"`
	Reason          string                                            `json:"reason,omitempty"`
	Counts          notificationRetryWaitCounts                       `json:"counts"`
	LastObservation *api.EventRecoveryNotificationRetryDecisionDetail `json:"last_observation,omitempty"`
}

type notificationRetryDecisionClient interface {
	GetEventRecoveryNotificationRetryDecision(context.Context, string, string) (api.EventRecoveryNotificationRetryDecisionDetail, error)
}

// The mutable delivery state must never change the request being waited on.
func notificationRetryObservationMatches(got api.EventRecoveryNotificationRetryDecisionDetail, pin *api.EventRecoveryNotificationRetryDecisionDetail, job, request string) bool {
	if got.JobID != job || got.RequestID != request || got.AppID == "" || got.DecidedAt.IsZero() || len(got.Decisions) == 0 || len(got.Decisions) > api.EventRecoveryNotificationRetryTargetsMax {
		return false
	}
	seen := make(map[string]bool, len(got.Decisions))
	for _, row := range got.Decisions {
		if row.Target.DeliveryID == "" || row.Target.WebhookID == "" || seen[row.Target.DeliveryID] || (row.Target.Kind != "admission" && row.Target.Kind != "execution") {
			return false
		}
		seen[row.Target.DeliveryID] = true
		if row.State == "queued" && (row.Target.ExpectedReplayGeneration == nil || row.ReplayGeneration == nil || *row.Target.ExpectedReplayGeneration < 0 || int64(*row.ReplayGeneration) != int64(*row.Target.ExpectedReplayGeneration)+1) {
			return false
		}
	}
	if pin == nil {
		return true
	}
	if got.AppID != pin.AppID || !got.DecidedAt.Equal(pin.DecidedAt) || len(got.Decisions) != len(pin.Decisions) {
		return false
	}
	for i, row := range got.Decisions {
		original := pin.Decisions[i]
		if !reflect.DeepEqual(row.Target, original.Target) || row.State != original.State || row.Reason != original.Reason || !reflect.DeepEqual(row.ReplayGeneration, original.ReplayGeneration) {
			return false
		}
	}
	return true
}

func notificationRetryWaitState(detail api.EventRecoveryNotificationRetryDecisionDetail) (notificationRetryWaitCounts, string, string) {
	var counts notificationRetryWaitCounts
	for _, row := range detail.Decisions {
		if row.State == "skipped" && row.RetryOutcome == "not_applicable" {
			counts.Skipped++
			continue
		}
		counts.Queued++
		if row.State != "queued" || row.ReplayGeneration == nil || *row.ReplayGeneration <= 0 {
			counts.Unknown++
			continue
		}
		switch row.RetryOutcome {
		case "succeeded", "failed":
			if row.CompletedAt == nil || row.CompletedAt.IsZero() {
				counts.Unknown++
			} else if row.RetryOutcome == "succeeded" {
				counts.Succeeded++
			} else {
				counts.Failed++
			}
		case "pending":
			counts.Pending++
		default:
			counts.Unknown++
		}
	}
	if counts.Failed > 0 {
		return counts, "failed", "receiver_failed"
	}
	if counts.Unknown > 0 {
		return counts, "inconclusive", "outcome_unknown"
	}
	if counts.Queued == 0 {
		return counts, "inconclusive", "no_queued_targets"
	}
	if counts.Pending > 0 {
		return counts, "pending", ""
	}
	return counts, "succeeded", ""
}

func notificationRetryWaitStopped(ctx context.Context, receipt *notificationRetryWaitReceipt) bool {
	switch ctx.Err() {
	case nil:
		return false
	case context.DeadlineExceeded:
		receipt.Status, receipt.Reason = "timed_out", "wait_deadline_exceeded"
	default:
		receipt.Status, receipt.Reason = "cancelled", "interrupted"
	}
	return true
}

func waitRecoveryNotificationRetry(ctx context.Context, client notificationRetryDecisionClient, job, request string) (notificationRetryWaitReceipt, error) {
	receipt := notificationRetryWaitReceipt{JobID: job, RequestID: request}
	var pin *api.EventRecoveryNotificationRetryDecisionDetail
	for {
		if notificationRetryWaitStopped(ctx, &receipt) {
			return receipt, nil
		}
		readCtx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
		got, err := client.GetEventRecoveryNotificationRetryDecision(readCtx, job, request)
		cancel()
		if notificationRetryWaitStopped(ctx, &receipt) {
			return receipt, nil
		}
		if err != nil {
			receipt.Status, receipt.Reason = "error", "read_failed"
			return receipt, err
		}
		if !notificationRetryObservationMatches(got, pin, job, request) {
			receipt.Status, receipt.Reason = "error", "observation_changed"
			return receipt, fmt.Errorf("server returned an invalid or different retry decision")
		}
		if pin == nil {
			first := got
			pin = &first
		}
		counts, status, reason := notificationRetryWaitState(got)
		if receipt.LastObservation == nil || counts != receipt.Counts {
			_, _ = fmt.Fprintf(osStderr, "Recovery %s | request %s | succeeded %d | failed %d | pending %d | unknown %d | skipped %d\n", oneLine(job), oneLine(request), counts.Succeeded, counts.Failed, counts.Pending, counts.Unknown, counts.Skipped)
		}
		receipt.Counts, receipt.Status, receipt.Reason, receipt.LastObservation = counts, status, reason, &got
		if status != "pending" {
			return receipt, nil
		}
		timer := time.NewTimer(api.EventRecoveryNotificationRetryWaitPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			notificationRetryWaitStopped(ctx, &receipt)
			return receipt, nil
		case <-timer.C:
		}
	}
}

func cmdEventsRecoveryNotificationRetryWait(client notificationRetryDecisionClient, job, request string, timeout time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	receipt, err := waitRecoveryNotificationRetry(ctx, client, job, request)
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Retry wait: %s | reason: %s | recovery: %s | request: %s\n", oneLine(receipt.Status), oneLine(receipt.Reason), oneLine(job), oneLine(request))
		if receipt.LastObservation != nil {
			outputRecoveryNotificationRetryDecision(*receipt.LastObservation)
		}
	}
	if err != nil {
		return printErr("Notification retry wait failed; inspect the saved request again", err)
	}
	switch receipt.Status {
	case "succeeded":
		return 0
	case "inconclusive":
		return 2
	case "timed_out":
		return 3
	case "cancelled":
		return 130
	default:
		return 1
	}
}
