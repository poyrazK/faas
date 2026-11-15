package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentStabilitySource struct {
	kind, name, node, urlID, receiver, integration string
	budget, maxAge                                 float64
}
type obsDeploymentStabilityEvidence map[obsDeploymentStabilitySource]time.Time

// Source vectors stay bounded by the existing serving-pair, integration and
// receiver limits. Diagnostics project a bounded preview of reviewed identities.
func collectObsDeploymentStabilityEvidence(serving *api.ObsServingHealth, notifications *obsDeploymentNotifications) (obsDeploymentStabilityEvidence, bool) {
	if serving == nil || len(serving.Probes) == 0 || len(serving.Probes) > api.ObsServingProbePairsMax || notifications == nil || len(notifications.Integrations) == 0 || len(notifications.Integrations) > api.ObsNotificationIntegrationsMax || notifications.ReceiverHealth == nil || len(notifications.ReceiverHealth.Groups) == 0 || len(notifications.ReceiverHealth.Groups) > api.ObsNotificationIntegrationsMax {
		return nil, false
	}
	out := obsDeploymentStabilityEvidence{}
	valid := true
	add := func(key obsDeploymentStabilitySource, at *time.Time) {
		if _, duplicate := out[key]; duplicate || at == nil || at.IsZero() {
			valid = false
			return
		}
		out[key] = *at
	}
	add(obsDeploymentStabilitySource{kind: "serving_inventory"}, serving.Inventory.ObservedAt)
	for _, probe := range serving.Probes {
		key := obsDeploymentStabilitySource{kind: "probe_evaluation", node: probe.Node, urlID: probe.URLID, budget: probe.ExpectedFirstByteSeconds, maxAge: probe.MaxResultAgeSeconds}
		add(key, probe.EvaluatedAt)
		key.kind = "probe_run"
		add(key, probe.LastRunAt)
	}
	add(obsDeploymentStabilitySource{kind: "alertmanager_api"}, notifications.AlertmanagerAPI.ObservedAt)
	for _, name := range deploymentSignalNames() {
		add(obsDeploymentStabilitySource{kind: "pipeline_signal", name: name}, notifications.Signals[name].ObservedAt)
	}
	for _, integration := range notifications.Integrations {
		add(obsDeploymentStabilitySource{kind: "integration_attempts", integration: integration.Integration}, integration.Attempts.ObservedAt)
		add(obsDeploymentStabilitySource{kind: "integration_failures", integration: integration.Integration}, integration.Failures.ObservedAt)
	}
	receivers := notifications.ReceiverHealth
	add(obsDeploymentStabilitySource{kind: "receiver_collector"}, receivers.Collector.ObservedAt)
	add(obsDeploymentStabilitySource{kind: "receiver_inventory"}, receivers.Inventory.ObservedAt)
	for _, group := range receivers.Groups {
		key := obsDeploymentStabilitySource{kind: "receiver_requests", receiver: group.Receiver, integration: group.Integration}
		add(key, group.Requests.ObservedAt)
		key.kind = "receiver_failures"
		add(key, group.Failures.ObservedAt)
		key.kind = "receiver_success_observation"
		add(key, group.LastSuccessObservation.ObservedAt)
	}
	return out, valid
}
