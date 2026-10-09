package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type EventRecoveryNotificationRetryStore interface {
	PreviewEventRecoveryNotificationRetry(context.Context, string, string, time.Time) (api.EventRecoveryNotificationRetryPreview, error)
	RetryEventRecoveryNotifications(context.Context, string, string, api.EventRecoveryNotificationRetryRequest, time.Time) (api.EventRecoveryNotificationRetryResponse, error)
}

var (
	ErrEventRecoveryNotificationRetryLimit            = fmt.Errorf("recovery notification retry decision limit reached")
	ErrEventRecoveryNotificationRetryDecisionNotFound = fmt.Errorf("recovery notification retry decision not found")
)

type recoveryNotificationRetryReceipt struct {
	ActorKind string                                     `json:"actor_kind"`
	ActorID   string                                     `json:"actor_id"`
	Request   api.EventRecoveryNotificationRetryRequest  `json:"request"`
	Response  api.EventRecoveryNotificationRetryResponse `json:"response"`
}

func recoveryNotificationRetryPrior(receipts map[string]json.RawMessage, req api.EventRecoveryNotificationRetryRequest) (api.EventRecoveryNotificationRetryResponse, bool, error) {
	raw, ok := receipts[req.RequestID]
	if !ok {
		return api.EventRecoveryNotificationRetryResponse{}, false, nil
	}
	var saved recoveryNotificationRetryReceipt
	if err := json.Unmarshal(raw, &saved); err != nil {
		return saved.Response, true, err
	}
	if !reflect.DeepEqual(saved.Request, req) {
		return api.EventRecoveryNotificationRetryResponse{}, true, ErrEventRecoveryRequestConflict
	}
	return saved.Response, true, nil
}
func recoveryNotificationRetryReason(delivery string, status string, generation int, available, enabled, allowed bool, expected *int) string {
	switch {
	case delivery == "":
		return "delivery_unavailable"
	case !available:
		return "receiver_unavailable"
	case !enabled:
		return "receiver_disabled"
	case !allowed:
		return "plan_not_allowed"
	case expected != nil && generation != *expected:
		return "generation_changed"
	case status != "dead":
		return "not_dead"
	case generation >= math.MaxInt32:
		return "generation_exhausted"
	}
	return ""
}
func recoveryNotificationRetryPreview(report api.EventRecoveryNotifications, enabled map[string]bool, allowed bool) api.EventRecoveryNotificationRetryPreview {
	out := api.EventRecoveryNotificationRetryPreview{JobID: report.JobID, AppID: report.AppID, ObservedAt: report.ObservedAt, CountsComplete: true, Receivers: []api.EventRecoveryNotificationRetryCandidate{}}
	for _, notice := range report.Notifications {
		if notice.CaptureStatus == "pending" || notice.CaptureStatus == "not_applicable" {
			continue
		}
		if !notice.CountsComplete || notice.AcknowledgementStatus == "unknown" {
			out.CountsComplete = false
		}
		for _, r := range notice.Receivers {
			on, exists := enabled[r.WebhookID]
			reason := recoveryNotificationRetryReason(r.DeliveryID, r.Status, r.ReplayGeneration, exists, on, allowed, nil)
			out.Receivers = append(out.Receivers, api.EventRecoveryNotificationRetryCandidate{Kind: notice.Kind, WebhookID: r.WebhookID, DeliveryID: r.DeliveryID, ReplayGeneration: r.ReplayGeneration, Status: r.Status, Eligible: reason == "", Reason: reason})
		}
	}
	return out
}
func recoveryNotificationRetryReceiver(report api.EventRecoveryNotifications, target api.EventRecoveryNotificationRetryTarget) (api.EventRecoveryNotificationReceiver, string, string) {
	for _, notice := range report.Notifications {
		if notice.Kind != target.Kind {
			continue
		}
		for _, r := range notice.Receivers {
			if r.WebhookID == target.WebhookID {
				if r.DeliveryID != target.DeliveryID {
					return r, notice.Event, "delivery_changed"
				}
				return r, notice.Event, ""
			}
		}
	}
	return api.EventRecoveryNotificationReceiver{}, "", "delivery_unavailable"
}
func validateRecoveryNotificationRetry(account, id string, now time.Time, req *api.EventRecoveryNotificationRetryRequest) error {
	if err := eventRecoveryIDs(account, id); err != nil {
		return err
	}
	if now.IsZero() {
		return ErrEventRecoveryQuery
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	*req = req.Canonical()
	return nil
}
func recoveryNotificationRetryAllowed(plan api.Plan) bool {
	limits, ok := api.LimitsFor(plan)
	return ok && limits.WebhookPerApp > 0
}

type EventRecoveryNotificationRetryHistoryStore interface {
	GetEventRecoveryNotificationRetryHistory(context.Context, string, string, time.Time) (api.EventRecoveryNotificationRetryHistory, error)
	GetEventRecoveryNotificationRetryDecision(context.Context, string, string, string, time.Time) (api.EventRecoveryNotificationRetryDecisionDetail, error)
}

func recoveryNotificationRetryReceipts(raw []byte) (map[string]json.RawMessage, error) {
	receipts := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &receipts); err != nil {
		return nil, err
	}
	return receipts, nil
}
func recoveryNotificationRetryHistory(job, app string, now time.Time, receipts map[string]json.RawMessage) (api.EventRecoveryNotificationRetryHistory, error) {
	out := api.EventRecoveryNotificationRetryHistory{JobID: job, AppID: app, ObservedAt: now, Decisions: []api.EventRecoveryNotificationRetryDecisionSummary{}}
	for id, raw := range receipts {
		var saved recoveryNotificationRetryReceipt
		if err := json.Unmarshal(raw, &saved); err != nil {
			return out, err
		}
		if saved.Response.RequestID != id || saved.Response.JobID != job || saved.Response.AppID != app || saved.Response.DecidedAt.IsZero() {
			return out, fmt.Errorf("invalid retained notification retry decision")
		}
		summary := api.EventRecoveryNotificationRetryDecisionSummary{RequestID: id, DecidedAt: saved.Response.DecidedAt, TargetCount: len(saved.Response.Results)}
		for _, result := range saved.Response.Results {
			switch result.State {
			case "queued":
				summary.QueuedCount++
			case "skipped":
				summary.SkippedCount++
			default:
				return out, fmt.Errorf("invalid retained notification retry result")
			}
		}
		out.Decisions = append(out.Decisions, summary)
	}
	sort.Slice(out.Decisions, func(i, j int) bool {
		if out.Decisions[i].DecidedAt.Equal(out.Decisions[j].DecidedAt) {
			return out.Decisions[i].RequestID < out.Decisions[j].RequestID
		}
		return out.Decisions[i].DecidedAt.After(out.Decisions[j].DecidedAt)
	})
	return out, nil
}
func recoveryNotificationRetryDecisionDetail(job, app, requestID string, now time.Time, saved recoveryNotificationRetryReceipt, report api.EventRecoveryNotifications) (api.EventRecoveryNotificationRetryDecisionDetail, error) {
	response := saved.Response
	if response.RequestID != requestID || response.JobID != job || response.AppID != app || response.DecidedAt.IsZero() {
		return api.EventRecoveryNotificationRetryDecisionDetail{}, fmt.Errorf("invalid retained notification retry decision")
	}
	out := api.EventRecoveryNotificationRetryDecisionDetail{JobID: job, AppID: app, RequestID: requestID, DecidedAt: response.DecidedAt, CurrentStatusObservedAt: now, Decisions: []api.EventRecoveryNotificationRetryDecision{}}
	for _, result := range response.Results {
		row := api.EventRecoveryNotificationRetryDecision{Target: result.Target, State: result.State, Reason: result.Reason, ReplayGeneration: result.ReplayGeneration, CurrentDeliveryStatus: "unavailable"}
		for _, notice := range report.Notifications {
			if notice.Kind == result.Target.Kind {
				for _, receiver := range notice.Receivers {
					if receiver.WebhookID == result.Target.WebhookID && receiver.DeliveryID == result.Target.DeliveryID {
						row.CurrentDeliveryStatus = receiver.Status
						n := receiver.ReplayGeneration
						row.CurrentReplayGeneration = &n
					}
				}
			}
		}
		out.Decisions = append(out.Decisions, row)
	}
	return out, nil
}
