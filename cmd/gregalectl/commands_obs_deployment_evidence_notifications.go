package main

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func obsDeploymentPipelineEvidence(raw api.ObsNotificationHealth, out obsDeploymentNotifications, after, now time.Time) []obsDeploymentEvidenceDetail {
	items := []obsDeploymentEvidenceDetail{}
	snapshot := obsDeploymentEvidenceClock("notification_snapshot", &out.GeneratedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
	if snapshot.Reason != "" {
		return append(items, snapshot)
	}
	if raw.WindowSeconds != int(api.ObsNotificationWindow.Seconds()) {
		items = append(items, obsDeploymentEvidenceDetail{Kind: "pipeline_metadata", Name: "window_seconds", Reason: "unexpected_window"})
	}
	if len(raw.Signals) > len(deploymentSignalNames()) {
		items = append(items, obsDeploymentEvidenceDetail{Kind: "pipeline_metadata", Name: "signals", Reason: "unexpected_signal_set"})
	}
	apiStatus := obsDeploymentEvidenceClock("alertmanager_api", out.AlertmanagerAPI.ObservedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
	apiStatus.SourceState = out.AlertmanagerAPI.State
	if out.AlertmanagerAPI.Reason == "invalid_value" {
		apiStatus.Reason = "invalid_metadata"
	}
	if apiStatus.Reason == "" && out.AlertmanagerAPI.State != "healthy" {
		apiStatus.Reason = "api_not_ready"
	}
	if apiStatus.Reason != "" {
		items = append(items, apiStatus)
	}
	for _, name := range deploymentSignalNames() {
		signal, exists := raw.Signals[name]
		if !exists {
			items = append(items, obsDeploymentEvidenceDetail{Kind: "pipeline_signal", Name: name, Reason: "missing_signal"})
			continue
		}
		window := time.Duration(0)
		if slices.Contains([]string{"prometheus_dropped", "alertmanager_attempts", "alertmanager_failures"}, name) {
			window = api.ObsNotificationWindow
		}
		if item := obsDeploymentEvidenceSignal("pipeline_signal", name, signal, window, after, now); item != nil {
			items = append(items, *item)
		}
	}
	if len(raw.Integrations) == 0 {
		items = append(items, obsDeploymentEvidenceDetail{Kind: "integration_inventory", Reason: "expectations_unavailable"})
	} else if invalidObsDeploymentIntegrationMetadata(raw.Integrations) {
		items = append(items, obsDeploymentEvidenceDetail{Kind: "integration_inventory", Reason: "invalid_metadata"})
	}
	for _, integration := range out.Integrations {
		original := integration
		for i, candidate := range raw.Integrations {
			if i >= api.ObsNotificationIntegrationsMax {
				break
			}
			if candidate.Integration == integration.Integration {
				original = candidate
				break
			}
		}
		for _, signal := range []struct {
			name  string
			value api.ObsNotificationSignal
		}{{"attempts", original.Attempts}, {"failures", original.Failures}} {
			if item := obsDeploymentEvidenceSignal("integration_signal", signal.name, signal.value, api.ObsNotificationWindow, after, now); item != nil {
				item.Integration = integration.Integration
				items = append(items, *item)
			}
		}
	}
	return items
}

func invalidObsDeploymentIntegrationMetadata(integrations []api.ObsNotificationIntegration) bool {
	if len(integrations) > api.ObsNotificationIntegrationsMax {
		return true
	}
	seen := map[string]bool{}
	for _, integration := range integrations {
		if seen[integration.Integration] || !slices.Contains(operatorobs.NotificationIntegrationNames(), integration.Integration) {
			return true
		}
		seen[integration.Integration] = true
	}
	return false
}

func obsDeploymentReceiverEvidence(raw api.ObsNotificationHealth, out obsDeploymentNotifications, after, now time.Time) []obsDeploymentEvidenceDetail {
	items := []obsDeploymentEvidenceDetail{}
	snapshot := obsDeploymentEvidenceClock("notification_snapshot", &out.GeneratedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
	if snapshot.Reason != "" {
		return append(items, snapshot)
	}
	data := out.ReceiverHealth
	if data == nil {
		reason := "receiver_evidence_missing"
		if raw.ReceiverHealth != nil {
			reason = "invalid_metadata"
		}
		return append(items, obsDeploymentEvidenceDetail{Kind: "receiver_metadata", Reason: reason})
	}
	for _, source := range []struct {
		kind   string
		signal api.ObsNotificationSignal
	}{{"receiver_collector", data.Collector}, {"receiver_inventory", data.Inventory}} {
		item := obsDeploymentEvidenceSignal(source.kind, "", source.signal, 0, after, now)
		if item != nil {
			items = append(items, *item)
		}
	}
	if data.Collector.State == "healthy" && data.Collector.Value != nil && *data.Collector.Value != 1 && obsDeploymentFresh(data.Collector.ObservedAt, after, now) {
		item := obsDeploymentEvidenceClock("receiver_collector", data.Collector.ObservedAt, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
		item.Reason, item.SourceState = "collector_not_ready", data.Collector.State
		items = append(items, item)
	}
	if len(items) > 0 {
		return items
	}
	for _, group := range data.Groups {
		before := len(items)
		if group.Expected == nil || !*group.Expected {
			reason := "expectations_unavailable"
			if group.Expected != nil {
				reason = "receiver_not_expected"
			}
			items = append(items, obsDeploymentEvidenceDetail{Kind: "receiver_group", Receiver: group.Receiver, Integration: group.Integration, Reason: reason, SourceState: group.State, SourceReason: group.Reason})
			continue
		}
		for _, source := range []struct {
			kind   string
			signal api.ObsNotificationSignal
			window time.Duration
		}{
			{"receiver_requests", group.Requests, api.ObsNotificationWindow}, {"receiver_failures", group.Failures, api.ObsNotificationWindow}, {"receiver_success_observation", group.LastSuccessObservation, 0},
		} {
			if item := obsDeploymentEvidenceSignal(source.kind, "", source.signal, source.window, after, now); item != nil {
				item.Receiver, item.Integration = group.Receiver, group.Integration
				items = append(items, *item)
			}
		}
		if group.State != "healthy" && len(items) == before {
			items = append(items, obsDeploymentEvidenceDetail{Kind: "receiver_group", Receiver: group.Receiver, Integration: group.Integration, Reason: "incomplete_group_evidence", SourceState: group.State, SourceReason: group.Reason})
		}
	}
	return items
}
