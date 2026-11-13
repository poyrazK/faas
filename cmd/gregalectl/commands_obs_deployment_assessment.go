package main

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func obsDeploymentCheckFor(name string) obsDeploymentCheck {
	runbook := "https://github.com/poyrazK/faas/blob/main/docs/runbooks/FaasAlertmanagerDeliveryDegraded.md"
	if name == "serving_probes" {
		runbook = "https://github.com/poyrazK/faas/blob/main/docs/ops/serving-health.md"
	}
	return obsDeploymentCheck{Name: name, State: "unknown", Reason: "source_unavailable", Runbook: runbook}
}

func assessObsDeployment(snapshot obsDeploymentSnapshot, after, now time.Time) ([]obsDeploymentCheck, *api.ObsServingHealth, *obsDeploymentNotifications) {
	servingCheck := obsDeploymentCheckFor("serving_probes")
	var serving *api.ObsServingHealth
	if snapshot.serving.err == nil && validObsServingHealth(snapshot.serving.value) {
		value := snapshot.serving.value
		serving = &value
		servingCheck = assessDeploymentServing(value, after, now)
	}
	aggregate, receivers := obsDeploymentCheckFor("notification_pipeline"), obsDeploymentCheckFor("notification_receivers")
	var notification *obsDeploymentNotifications
	if snapshot.notificationErr == nil && !snapshot.notifications.GeneratedAt.IsZero() {
		value, complete := projectDeploymentNotifications(snapshot.notifications)
		notification = &value
		aggregate = assessDeploymentNotification(value, complete, after, now)
		receivers = assessDeploymentReceivers(value, after, now)
	}
	return []obsDeploymentCheck{servingCheck, aggregate, receivers}, serving, notification
}

func assessDeploymentServing(out api.ObsServingHealth, after, now time.Time) obsDeploymentCheck {
	check := obsDeploymentCheckFor("serving_probes")
	check.Reason = "awaiting_post_deployment_evidence"
	if !obsDeploymentFresh(&out.GeneratedAt, after, now) || !obsDeploymentFresh(out.Inventory.ObservedAt, after, now) {
		return check
	}
	if out.Inventory.Configured == nil || !*out.Inventory.Configured || out.Counts == nil || out.Counts.Expected == 0 {
		check.Reason = "serving_expectations_unavailable"
		return check
	}
	complete := true
	for _, probe := range out.Probes {
		if !obsDeploymentFresh(probe.EvaluatedAt, after, now) {
			complete = false
			continue
		}
		if probe.State != "healthy" && probe.State != "unknown" {
			check.State, check.Reason = "degraded", "serving_probe_fault"
			return check
		}
		if probe.State != "healthy" || !obsDeploymentFreshRun(probe.LastRunAt, after, now, probe.MaxResultAgeSeconds) {
			complete = false
		}
	}
	if complete {
		check.State, check.Reason = "passed", ""
	}
	return check
}

func obsDeploymentFreshRun(at *time.Time, after, now time.Time, maxAge float64) bool {
	return at != nil && !at.IsZero() && !at.Before(after) && !at.After(now.Add(api.ObsHealthClockSkewTolerance)) && float64(now.UnixMilli()-at.UnixMilli())/1000 <= maxAge
}

func deploymentSignalNames() []string {
	return []string{"alertmanager_up", "alertmanager_config_reload_successful", "prometheus_queue_length", "prometheus_queue_capacity", "prometheus_dropped", "alertmanager_attempts", "alertmanager_failures"}
}

func deploymentSignal(signal api.ObsNotificationSignal, window time.Duration, at time.Time) api.ObsNotificationSignal {
	if signal.Reason == "incomplete_integration_evidence" {
		signal.Reason = "incomplete_evidence"
	}
	if validReceiverSignal(signal, window, at) {
		return signal
	}
	return api.ObsNotificationSignal{State: "unavailable", Reason: "invalid_value", WindowSeconds: int(window.Seconds()), MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds())}
}

// Project only bounded, reviewed fields. Unknown labels, provider errors and
// arbitrary limitation strings never enter the report artifact.
func projectDeploymentNotifications(out api.ObsNotificationHealth) (obsDeploymentNotifications, bool) {
	projected := obsDeploymentNotifications{GeneratedAt: out.GeneratedAt, Signals: map[string]api.ObsNotificationSignal{}, Integrations: []api.ObsNotificationIntegration{}, AlertmanagerAPI: api.ObsNotificationAPIStatus{State: "unavailable", Reason: "invalid_value"}}
	complete := out.WindowSeconds == int(api.ObsNotificationWindow.Seconds()) && len(out.Signals) == len(deploymentSignalNames()) && len(out.Integrations) > 0 && len(out.Integrations) <= api.ObsNotificationIntegrationsMax
	if slices.Contains([]string{"healthy", "disabled", "unavailable"}, out.AlertmanagerAPI.State) {
		projected.AlertmanagerAPI.State = out.AlertmanagerAPI.State
		projected.AlertmanagerAPI.Reason = ""
		projected.AlertmanagerAPI.ObservedAt = out.AlertmanagerAPI.ObservedAt
	}
	for _, name := range deploymentSignalNames() {
		window := time.Duration(0)
		if slices.Contains([]string{"prometheus_dropped", "alertmanager_attempts", "alertmanager_failures"}, name) {
			window = api.ObsNotificationWindow
		}
		signal, exists := out.Signals[name]
		if !exists {
			complete = false
		}
		projected.Signals[name] = deploymentSignal(signal, window, out.GeneratedAt)
	}
	seen := map[string]bool{}
	for i, integration := range out.Integrations {
		if i >= api.ObsNotificationIntegrationsMax {
			complete = false
			break
		}
		if !slices.Contains(operatorobs.NotificationIntegrationNames(), integration.Integration) || seen[integration.Integration] {
			complete = false
			continue
		}
		seen[integration.Integration] = true
		integration.Attempts = deploymentSignal(integration.Attempts, api.ObsNotificationWindow, out.GeneratedAt)
		integration.Failures = deploymentSignal(integration.Failures, api.ObsNotificationWindow, out.GeneratedAt)
		projected.Integrations = append(projected.Integrations, integration)
	}
	if out.ReceiverHealth != nil && validObsNotificationReceivers(*out.ReceiverHealth, out.GeneratedAt) {
		projected.ReceiverHealth = out.ReceiverHealth
	}
	return projected, complete
}

func deploymentSignalCurrent(signal api.ObsNotificationSignal, after, now time.Time) bool {
	return signal.State == "healthy" && signal.Value != nil && obsDeploymentFresh(signal.ObservedAt, after, now)
}

func assessDeploymentNotification(out obsDeploymentNotifications, complete bool, after, now time.Time) obsDeploymentCheck {
	check := obsDeploymentCheckFor("notification_pipeline")
	check.Reason = "awaiting_post_deployment_evidence"
	if !obsDeploymentFresh(&out.GeneratedAt, after, now) {
		return check
	}
	complete = complete && out.AlertmanagerAPI.State == "healthy" && obsDeploymentFresh(out.AlertmanagerAPI.ObservedAt, after, now)
	for name, signal := range out.Signals {
		if !deploymentSignalCurrent(signal, after, now) {
			complete = false
			continue
		}
		fault := false
		switch name {
		case "alertmanager_up", "alertmanager_config_reload_successful":
			fault = *signal.Value != 1
		case "prometheus_dropped", "alertmanager_failures":
			fault = *signal.Value > 0
		case "prometheus_queue_capacity":
			fault = *signal.Value == 0
		}
		if fault {
			check.State, check.Reason = "degraded", "notification_pipeline_fault"
			return check
		}
	}
	length, capacity := out.Signals["prometheus_queue_length"], out.Signals["prometheus_queue_capacity"]
	if deploymentSignalCurrent(length, after, now) && deploymentSignalCurrent(capacity, after, now) && *capacity.Value > 0 && *length.Value / *capacity.Value > api.ObsNotificationQueueBusyRatio {
		check.State, check.Reason = "degraded", "notification_queue_backlog"
		return check
	}
	for _, integration := range out.Integrations {
		if deploymentSignalCurrent(integration.Failures, after, now) && *integration.Failures.Value > 0 {
			check.State, check.Reason = "degraded", "notification_integration_fault"
			return check
		}
		complete = complete && deploymentSignalCurrent(integration.Attempts, after, now) && deploymentSignalCurrent(integration.Failures, after, now)
	}
	if complete {
		check.State, check.Reason = "passed", ""
	}
	return check
}

func assessDeploymentReceivers(out obsDeploymentNotifications, after, now time.Time) obsDeploymentCheck {
	check := obsDeploymentCheckFor("notification_receivers")
	check.Reason = "receiver_evidence_unavailable"
	data := out.ReceiverHealth
	if data == nil || !obsDeploymentFresh(&out.GeneratedAt, after, now) {
		return check
	}
	check.Reason = "awaiting_post_deployment_evidence"
	sourcesCurrent := deploymentSignalCurrent(data.Collector, after, now) && *data.Collector.Value == 1 && deploymentSignalCurrent(data.Inventory, after, now) && *data.Inventory.Value > 0
	complete := sourcesCurrent
	for _, group := range data.Groups {
		if deploymentSignalCurrent(group.Failures, after, now) && *group.Failures.Value > 0 {
			check.State, check.Reason = "degraded", "receiver_requests_failing"
			return check
		}
		if sourcesCurrent && group.State == "degraded" && slices.Contains([]string{"telemetry_missing", "unexpected_group"}, group.Reason) {
			check.State, check.Reason = "degraded", "receiver_coverage_fault"
			return check
		}
		complete = complete && group.Expected != nil && *group.Expected && group.State == "healthy" && deploymentSignalCurrent(group.Requests, after, now) && deploymentSignalCurrent(group.Failures, after, now) && deploymentSignalCurrent(group.LastSuccessObservation, after, now)
	}
	if complete && len(data.Groups) > 0 {
		check.State, check.Reason = "passed", ""
	}
	return check
}
