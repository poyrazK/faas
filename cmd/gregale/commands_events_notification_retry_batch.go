package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Plans retain the exact request IDs and generation guards supplied by the operator.
type notificationRetryPlan struct {
	Version    int                        `json:"version"`
	AppID      string                     `json:"app_id"`
	PreparedAt *time.Time                 `json:"prepared_at,omitempty"`
	Jobs       []notificationRetryPlanJob `json:"jobs"`
}
type notificationRetryPlanJob struct {
	JobID      string                                     `json:"job_id"`
	Request    api.EventRecoveryNotificationRetryRequest  `json:"request"`
	Preview    *api.EventRecoveryNotificationRetryPreview `json:"preview,omitempty"`
	Selections []notificationRetrySelection               `json:"selections,omitempty"`
}
type notificationRetrySelection struct {
	Target   api.EventRecoveryNotificationRetryTarget `json:"target"`
	Eligible bool                                     `json:"eligible"`
	Reason   string                                   `json:"reason,omitempty"`
}
type notificationRetryBatchReceipt struct {
	AppID string                         `json:"app_id"`
	Jobs  []notificationRetryBatchResult `json:"jobs"`
}
type notificationRetryBatchResult struct {
	JobID            string                                      `json:"job_id"`
	RequestID        string                                      `json:"request_id"`
	State            string                                      `json:"state"`
	Response         *api.EventRecoveryNotificationRetryResponse `json:"response,omitempty"`
	ReconcileCommand string                                      `json:"reconcile_command,omitempty"`
}

func readNotificationRetryPlan(path string, prepared bool) (notificationRetryPlan, error) {
	var plan notificationRetryPlan
	f, err := os.Open(path)
	if err != nil {
		return plan, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.EventRecoveryNotificationRetryBatchBodyMaxBytes+1))
	if err != nil {
		return plan, err
	}
	if len(raw) > api.EventRecoveryNotificationRetryBatchBodyMaxBytes {
		return plan, fmt.Errorf("plan exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&plan); err != nil {
		return plan, err
	}
	if d.Decode(new(any)) != io.EOF {
		return plan, fmt.Errorf("expected one JSON object")
	}
	canonicalID := func(s string) bool { id, e := uuid.Parse(s); return e == nil && id != uuid.Nil && id.String() == s }
	if plan.Version != 1 || !canonicalID(plan.AppID) || len(plan.Jobs) < 1 || len(plan.Jobs) > api.EventRecoveryNotificationRetryBatchJobsMax {
		return plan, fmt.Errorf("version 1, canonical app UUID and 1..%d jobs required", api.EventRecoveryNotificationRetryBatchJobsMax)
	}
	if prepared && (plan.PreparedAt == nil || plan.PreparedAt.IsZero()) {
		return plan, fmt.Errorf("use notification-retry-plan to prepare this selection first")
	}
	seen := map[string]bool{}
	for i := range plan.Jobs {
		j := &plan.Jobs[i]
		if !canonicalID(j.JobID) || seen[j.JobID] {
			return plan, fmt.Errorf("job IDs must be distinct canonical nonzero UUIDs")
		}
		seen[j.JobID] = true
		if err = j.Request.Validate(); err != nil {
			return plan, err
		}
		j.Request = j.Request.Canonical()
		if prepared && (j.Preview == nil || j.Preview.JobID != j.JobID || j.Preview.AppID != plan.AppID) {
			return plan, fmt.Errorf("plan preview identity mismatch")
		}
	}
	return plan, nil
}

func cmdEventsNotificationRetryBatch(args []string, apply bool) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("events notification-retry-plan/apply", flag.ContinueOnError)
	input := fs.String("file", "", "selection or prepared plan JSON")
	output := fs.String("output", "", "new plan file (required for preview)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 0 || *input == "" || (!apply && *output == "") || (apply && *output != "") || rejectUnexpectedFlagArgs(fs) {
		return printErr("Invalid arguments", fmt.Errorf("use --file PATH; preview also requires --output NEW_PATH"))
	}
	plan, err := readNotificationRetryPlan(*input, apply)
	if err != nil {
		return printErr("Invalid retry plan", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Validate every job's app before any writes. Eligibility remains advisory;
	// replaying an already saved request must still return its original receipt.
	for i := range plan.Jobs {
		j := &plan.Jobs[i]
		readCtx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
		preview, e := client.PreviewEventRecoveryNotificationRetry(readCtx, j.JobID)
		cancel()
		if e != nil {
			return printErr("Retry preflight failed; no requests submitted", e)
		}
		if preview.JobID != j.JobID || preview.AppID != plan.AppID {
			return printErr("Retry preflight failed; no requests submitted", fmt.Errorf("job/app identity mismatch"))
		}
		if !apply {
			j.Preview = &preview
			j.Selections = nil
			for _, t := range j.Request.Targets {
				s := notificationRetrySelection{Target: t, Reason: "delivery_unavailable"}
				for _, r := range preview.Receivers {
					if r.DeliveryID != t.DeliveryID {
						continue
					}
					s.Reason = r.Reason
					switch {
					case r.Kind != t.Kind || r.WebhookID != t.WebhookID:
						s.Reason = "delivery_changed"
					case r.ReplayGeneration != *t.ExpectedReplayGeneration:
						s.Reason = "generation_changed"
					default:
						s.Eligible = r.Eligible
					}
					break
				}
				j.Selections = append(j.Selections, s)
			}
		}
	}
	if !apply {
		now := time.Now().UTC()
		plan.PreparedAt = &now
		raw, e := json.MarshalIndent(plan, "", "  ")
		if e != nil {
			return printErr("Cannot encode plan", e)
		}
		if len(raw)+1 > api.EventRecoveryNotificationRetryBatchBodyMaxBytes {
			return printErr("Cannot save plan", fmt.Errorf("prepared plan exceeds size limit; select fewer jobs or targets"))
		}
		f, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return printErr("Cannot create plan", e)
		}
		_, e = f.Write(append(raw, '\n'))
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			_ = os.Remove(*output)
			return printErr("Cannot save plan", e)
		}
		return jsonOut(writeJSON(plan))
	}
	receipt := notificationRetryBatchReceipt{AppID: plan.AppID, Jobs: make([]notificationRetryBatchResult, len(plan.Jobs))}
	for i, j := range plan.Jobs {
		receipt.Jobs[i] = notificationRetryBatchResult{JobID: j.JobID, RequestID: j.Request.RequestID, State: "not_attempted"}
	}
	exit := 0
	for i, j := range plan.Jobs {
		if ctx.Err() != nil {
			exit = 130
			break
		}
		r := &receipt.Jobs[i]
		r.ReconcileCommand = fmt.Sprintf("gregale events recovery-notification-retry-history %s --request-id %s --json", j.JobID, j.Request.RequestID)
		writeCtx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
		response, e := client.RetryEventRecoveryNotifications(writeCtx, j.JobID, j.Request)
		cancel()
		valid := e == nil && response.JobID == j.JobID && response.AppID == plan.AppID && response.RequestID == j.Request.RequestID && len(response.Results) == len(j.Request.Targets)
		if valid {
			targets := make([]api.EventRecoveryNotificationRetryTarget, 0, len(response.Results))
			for _, result := range response.Results {
				targets = append(targets, result.Target)
				if result.State != "queued" && result.State != "skipped" {
					valid = false
				}
			}
			got := api.EventRecoveryNotificationRetryRequest{RequestID: j.Request.RequestID, Targets: targets}
			valid = valid && got.Validate() == nil && reflect.DeepEqual(got.Canonical(), j.Request)
		}
		if !valid {
			// Even an HTTP error may follow a committed transaction. Do not infer
			// failure or silently submit subsequent jobs after an uncertain response.
			r.State = "needs_reconciliation"
			exit = 2
			if ctx.Err() != nil {
				exit = 130
			}
			break
		}
		r.State = "decided"
		r.Response = &response
	}
	if e := writeJSON(receipt); e != nil {
		return printErr("Cannot write batch receipt; reconcile using plan request IDs", e)
	}
	return exit
}
