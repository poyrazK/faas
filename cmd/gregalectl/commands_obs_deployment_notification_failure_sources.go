package main

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func obsDeploymentPipelineFailures(out *obsDeploymentNotifications, after, now time.Time) []obsDeploymentNotificationFailure {
	var items []obsDeploymentNotificationFailure
	for _, name := range deploymentSignalNames() {
		signal := out.Signals[name]
		window := time.Duration(0)
		if slices.Contains([]string{"prometheus_dropped", "alertmanager_attempts", "alertmanager_failures"}, name) {
			window = api.ObsNotificationWindow
		}
		if !obsDeploymentNotificationFaultSignalCurrent(signal, window, after, now) {
			continue
		}
		reason := ""
		switch name {
		case "alertmanager_up", "alertmanager_config_reload_successful":
			if *signal.Value != 1 {
				reason = "value_not_one"
			}
		case "prometheus_dropped", "alertmanager_failures":
			if *signal.Value > 0 {
				reason = "positive_counter_increase"
			}
		case "prometheus_queue_capacity":
			if *signal.Value == 0 {
				reason = "queue_capacity_zero"
			}
		}
		if reason != "" {
			items = append(items, obsDeploymentNotificationFailure{Kind: "pipeline_signal", Name: name, Reason: reason, Signals: []obsDeploymentNotificationFailureSignal{obsDeploymentNotificationFailureSignalFor(name, signal)}})
		}
	}
	length, capacity := out.Signals["prometheus_queue_length"], out.Signals["prometheus_queue_capacity"]
	if obsDeploymentNotificationFaultSignalCurrent(length, 0, after, now) && obsDeploymentNotificationFaultSignalCurrent(capacity, 0, after, now) && *capacity.Value > 0 && *length.Value / *capacity.Value > api.ObsNotificationQueueBusyRatio {
		threshold := api.ObsNotificationQueueBusyRatio
		items = append(items, obsDeploymentNotificationFailure{Kind: "pipeline_queue", Reason: "queue_busy", Threshold: &threshold, Signals: []obsDeploymentNotificationFailureSignal{obsDeploymentNotificationFailureSignalFor("prometheus_queue_length", length), obsDeploymentNotificationFailureSignalFor("prometheus_queue_capacity", capacity)}})
	}
	seen := map[string]bool{}
	for i, integration := range out.Integrations {
		if i >= api.ObsNotificationIntegrationsMax {
			break
		}
		if seen[integration.Integration] || !slices.Contains(operatorobs.NotificationIntegrationNames(), integration.Integration) {
			continue
		}
		seen[integration.Integration] = true
		if obsDeploymentNotificationFaultSignalCurrent(integration.Failures, api.ObsNotificationWindow, after, now) && *integration.Failures.Value > 0 {
			items = append(items, obsDeploymentNotificationFailure{Kind: "integration", Integration: integration.Integration, Reason: "positive_counter_increase", Signals: []obsDeploymentNotificationFailureSignal{obsDeploymentNotificationFailureSignalFor("failures", integration.Failures)}})
		}
	}
	return items
}

func obsDeploymentReceiverFailures(out *obsDeploymentNotifications, after, now time.Time) []obsDeploymentNotificationFailure {
	data := out.ReceiverHealth
	if data == nil || !validObsNotificationReceivers(*data, out.GeneratedAt) {
		return nil
	}
	sourcesCurrent := obsDeploymentNotificationFaultSignalCurrent(data.Collector, 0, after, now) && *data.Collector.Value == 1 && obsDeploymentNotificationFaultSignalCurrent(data.Inventory, 0, after, now) && *data.Inventory.Value > 0
	var items []obsDeploymentNotificationFailure
	for _, group := range data.Groups {
		item := obsDeploymentNotificationFailure{Kind: "receiver", Receiver: group.Receiver, Integration: group.Integration, Expected: obsDeploymentEvidencePtr(group.Expected)}
		if obsDeploymentNotificationFaultSignalCurrent(group.Failures, api.ObsNotificationWindow, after, now) && *group.Failures.Value > 0 {
			item.Reason = "requests_failing"
			item.Signals = []obsDeploymentNotificationFailureSignal{obsDeploymentNotificationFailureSignalFor("failures", group.Failures)}
		} else if sourcesCurrent && group.State == "degraded" && slices.Contains([]string{"telemetry_missing", "unexpected_group"}, group.Reason) {
			// Coverage is the server's validated group assessment, supported by
			// current collection and intent. Do not turn absent counters into zero
			// or copy the collector's historical successful-delivery value.
			item.Reason = group.Reason
			item.Signals = []obsDeploymentNotificationFailureSignal{obsDeploymentNotificationFailureSignalFor("collector", data.Collector), obsDeploymentNotificationFailureSignalFor("inventory", data.Inventory)}
		} else {
			continue
		}
		items = append(items, item)
	}
	return items
}
