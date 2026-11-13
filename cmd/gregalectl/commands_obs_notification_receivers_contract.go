package main

import (
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsNotificationReceivers(out api.ObsNotificationReceiverHealth, now time.Time) bool {
	if out.Groups == nil || len(out.Groups) > api.ObsNotificationIntegrationsMax || !reflect.DeepEqual(out.Limitations, operatorobs.NotificationReceiverLimitations()) || !validReceiverReason(out.Reason) {
		return false
	}
	if !validReceiverSignal(out.Collector, 0, now) || !validReceiverSignal(out.Inventory, 0, now) {
		return false
	}
	if out.Collector.Value != nil && *out.Collector.Value != 0 && *out.Collector.Value != 1 {
		return false
	}
	if v := out.Inventory.Value; v != nil && (*v < 1 || *v > api.ObsNotificationIntegrationsMax || *v != math.Trunc(*v)) {
		return false
	}
	state, expected, previous := "healthy", 0, ""
	if out.Collector.Value == nil || *out.Collector.Value != 1 || out.Inventory.Value == nil {
		state = "partial"
	}
	for _, group := range out.Groups {
		key := group.Receiver + "/" + group.Integration
		if key <= previous || !validReceiverGroup(group, out, now) {
			return false
		}
		previous = key
		if group.Expected != nil && *group.Expected {
			expected++
		}
		if group.State == "degraded" || state == "degraded" {
			state = "degraded"
		} else if group.State == "partial" {
			state = "partial"
		}
	}
	if out.Inventory.Value != nil && *out.Inventory.Value != float64(expected) {
		return false
	}
	return out.State == state && (state != "healthy" || out.Reason == "")
}

func validReceiverReason(reason string) bool {
	return slices.Contains(operatorobs.NotificationReceiverReasons(), reason)
}

func validReceiverSignal(signal api.ObsNotificationSignal, window time.Duration, now time.Time) bool {
	if signal.WindowSeconds != int(window.Seconds()) || signal.MaxAgeSeconds != int(api.ObsHealthFreshnessMaxAge.Seconds()) || !validReceiverReason(signal.Reason) {
		return false
	}
	if signal.ObservedAt != nil && (signal.ObservedAt.IsZero() || signal.ObservedAt.After(now.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	switch signal.State {
	case api.ObsHealthSignalHealthy:
		return signal.Reason == "" && signal.Value != nil && *signal.Value >= 0 && !math.IsNaN(*signal.Value) && !math.IsInf(*signal.Value, 0) && signal.ObservedAt != nil && !signal.ObservedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge))
	case api.ObsHealthSignalUnavailable, api.ObsHealthSignalNoSamples, api.ObsHealthSignalStale:
		return signal.Value == nil && signal.Reason != "" && (signal.State != api.ObsHealthSignalStale || signal.ObservedAt != nil)
	default:
		return false
	}
}

func validReceiverGroup(group api.ObsNotificationReceiver, out api.ObsNotificationReceiverHealth, now time.Time) bool {
	if !slices.Contains(operatorobs.NotificationReceiverNames(), group.Receiver) || !slices.Contains(operatorobs.NotificationIntegrationNames(), group.Integration) || !validReceiverReason(group.Reason) || !slices.Contains([]string{"healthy", "partial", "degraded"}, group.State) {
		return false
	}
	if !validReceiverSignal(group.Requests, api.ObsNotificationWindow, now) || !validReceiverSignal(group.Failures, api.ObsNotificationWindow, now) || !validReceiverSignal(group.LastSuccessObservation, 0, now) {
		return false
	}
	if (group.Expected == nil) != (out.Inventory.Value == nil) {
		return false
	}
	if clock := group.LastSuccessObservation.Value; clock != nil && (*clock <= 0 || *clock > float64(now.Add(api.ObsHealthClockSkewTolerance).UnixMilli())/1000) {
		return false
	}
	if group.Failures.Value != nil && *group.Failures.Value > 0 && group.State != "degraded" {
		return false
	}
	if group.State == "healthy" {
		return group.Reason == "" && group.Expected != nil && *group.Expected && out.Collector.Value != nil && *out.Collector.Value == 1 && group.Requests.Value != nil && group.Failures.Value != nil && *group.Failures.Value == 0 && group.LastSuccessObservation.Value != nil
	}
	return group.Reason != ""
}
