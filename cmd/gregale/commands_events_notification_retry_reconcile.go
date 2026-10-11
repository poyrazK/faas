package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type notificationRetryReconciliation struct {
	AppID  string                               `json:"app_id"`
	Status string                               `json:"status"`
	Counts notificationRetryWaitCounts          `json:"counts"`
	Jobs   []notificationRetryReconciliationJob `json:"jobs"`
}
type notificationRetryReconciliationJob struct {
	JobID              string                                            `json:"job_id"`
	RequestID          string                                            `json:"request_id"`
	State              string                                            `json:"state"`
	Status             string                                            `json:"status"`
	Reason             string                                            `json:"reason,omitempty"`
	ObservationCurrent bool                                              `json:"observation_current"`
	Counts             notificationRetryWaitCounts                       `json:"counts"`
	LastObservation    *api.EventRecoveryNotificationRetryDecisionDetail `json:"last_observation,omitempty"`
}

// Match the entire saved intent, not just request identity or current delivery state.
func notificationRetryReconcileMatches(got api.EventRecoveryNotificationRetryDecisionDetail, pin *api.EventRecoveryNotificationRetryDecisionDetail, app string, job notificationRetryPlanJob) bool {
	if got.AppID != app || !notificationRetryObservationMatches(got, pin, job.JobID, job.Request.RequestID) {
		return false
	}
	targets := make([]api.EventRecoveryNotificationRetryTarget, 0, len(got.Decisions))
	for _, row := range got.Decisions {
		targets = append(targets, row.Target)
		if row.State != "queued" && row.State != "skipped" {
			return false
		}
		if row.State == "skipped" && (row.ReplayGeneration != nil || row.RetryOutcome != "not_applicable") {
			return false
		}
	}
	request := api.EventRecoveryNotificationRetryRequest{RequestID: got.RequestID, Targets: targets}
	return request.Validate() == nil && reflect.DeepEqual(request.Canonical(), job.Request)
}

func reconcileNotificationRetryPlan(ctx context.Context, client notificationRetryDecisionClient, plan notificationRetryPlan, wait bool) notificationRetryReconciliation {
	out := notificationRetryReconciliation{AppID: plan.AppID, Jobs: make([]notificationRetryReconciliationJob, len(plan.Jobs))}
	pins := make([]*api.EventRecoveryNotificationRetryDecisionDetail, len(plan.Jobs))
	for i, j := range plan.Jobs {
		out.Jobs[i] = notificationRetryReconciliationJob{JobID: j.JobID, RequestID: j.Request.RequestID, State: "not_observed", Status: "inconclusive", Reason: "not_observed"}
	}
	for {
		for i := range out.Jobs {
			out.Jobs[i].ObservationCurrent = false
		}
		for i, j := range plan.Jobs {
			if ctx.Err() != nil {
				break
			}
			row := &out.Jobs[i]
			row.ObservationCurrent = false
			readCtx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
			got, err := client.GetEventRecoveryNotificationRetryDecision(readCtx, j.JobID, j.Request.RequestID)
			cancel()
			if err != nil {
				row.State, row.Status, row.Reason = "read_failed", "error", "read_failed"
				var problem *api.APIError
				if errors.As(err, &problem) && problem.Problem.Status == 404 {
					row.State, row.Status, row.Reason = "missing", "inconclusive", "decision_not_retained_or_unavailable"
				}
				continue
			}
			if !notificationRetryReconcileMatches(got, pins[i], plan.AppID, j) {
				row.State, row.Status, row.Reason = "invalid_observation", "error", "identity_or_intent_changed"
				continue
			}
			if pins[i] == nil {
				first := got
				pins[i] = &first
			}
			row.State = "decided"
			row.ObservationCurrent = true
			row.Counts, row.Status, row.Reason = notificationRetryWaitState(got)
			row.LastObservation = &got
		}
		// Aggregate only current validated observations; stale evidence stays in each
		// job for inspection but is never counted as a fresh successful outcome.
		out.Counts = notificationRetryWaitCounts{}
		failed, unknown, pending, readError := false, false, false, false
		for _, row := range out.Jobs {
			switch row.Status {
			case "failed":
				failed = true
			case "inconclusive":
				unknown = true
			case "pending":
				pending = true
			case "error":
				readError = true
			}
			if !row.ObservationCurrent {
				continue
			}
			c := row.Counts
			out.Counts.Queued += c.Queued
			out.Counts.Skipped += c.Skipped
			out.Counts.Pending += c.Pending
			out.Counts.Succeeded += c.Succeeded
			out.Counts.Failed += c.Failed
			out.Counts.Unknown += c.Unknown
		}
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			out.Status = "timed_out"
		case ctx.Err() != nil:
			out.Status = "cancelled"
		case readError:
			out.Status = "error"
		case failed:
			out.Status = "failed"
		case unknown:
			out.Status = "inconclusive"
		case pending:
			out.Status = "pending"
		default:
			out.Status = "succeeded"
		}
		if !wait || out.Status != "pending" {
			return out
		}
		timer := time.NewTimer(api.EventRecoveryNotificationRetryWaitPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func cmdEventsNotificationRetryReconcile(args []string) int {
	flags, pos := splitArgsForFlags(args, "wait")
	fs := newFlagSet("events notification-retry-reconcile", flag.ContinueOnError)
	path := fs.String("file", "", "prepared retry plan JSON")
	wait := fs.Bool("wait", false, "wait for pending original-generation outcomes")
	timeout := fs.Duration("timeout", api.EventRecoveryNotificationRetryWaitTimeout, "overall read/wait deadline (default 5m)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	timeoutSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			timeoutSet = true
		}
	})
	if len(pos) != 0 || *path == "" || rejectUnexpectedFlagArgs(fs) || *timeout <= 0 || (timeoutSet && !*wait) {
		return printErr("Invalid reconciliation arguments", fmt.Errorf("use --file PLAN [--wait [--timeout DURATION]]; timeout must be positive"))
	}
	plan, err := readNotificationRetryPlan(*path, true)
	if err != nil {
		return printErr("Invalid retry plan", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	receipt := reconcileNotificationRetryPlan(ctx, client, plan, *wait)
	if err := writeJSON(receipt); err != nil {
		return printErr("Cannot write reconciliation receipt", err)
	}
	switch receipt.Status {
	case "succeeded":
		return 0
	case "pending", "inconclusive":
		return 2
	case "timed_out":
		return 3
	case "cancelled":
		return 130
	default:
		return 1
	}
}
