package state

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type EventRecoveryNotificationRetryStore interface {
	PreviewEventRecoveryNotificationRetry(context.Context, string, string, time.Time) (api.EventRecoveryNotificationRetryPreview, error)
	RetryEventRecoveryNotifications(context.Context, string, string, api.EventRecoveryNotificationRetryRequest, time.Time) (api.EventRecoveryNotificationRetryResponse, error)
}

var ErrEventRecoveryNotificationRetryLimit = fmt.Errorf("recovery notification retry decision limit reached")

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
