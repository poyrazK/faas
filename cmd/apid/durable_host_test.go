// adr: 977
package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestDurableEntityMetricsFollowOpsMetricsRebinding(t *testing.T) {
	s := &server{}
	s.WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid"))
	if s.durable.Metrics == nil {
		t.Fatal("metrics hook did not bind collectors")
	}
	s.WithOpsMetrics(context.Background(), nil)
	if s.durable.Metrics != nil {
		t.Fatal("metrics hook did not clear collectors")
	}
}

func TestValidatorReleasePreflightRunsWithAPIContractDiffDisabled(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	app := state.App{ID: uuid.NewString()}
	deployment := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: "default"}
	s := &server{executionAPIEnabled: true}
	s.durable = durable.Service{Host: durableHost{s}, ValidatorReleaseGateEnabled: true, RestoreIsolationEnabled: true, Apps: map[string]bool{app.ID: true}}
	if _, problem := s.contractTrafficContext(t.Context(), app, deployment); problem == nil || problem.Code != "durable_entity_validator_release_required" {
		t.Fatal("missing bundle bypassed preflight", problem)
	}
}

// durableMetric reads one durable entity series from registry by its full
// name and label pairs, so apid tests need no access to package internals.
func durableMetric(t *testing.T, registry *prometheus.Registry, name string, labels ...string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if durableMetricLabelsMatch(metric, labels) {
				return durableMetricValue(metric)
			}
		}
	}
	t.Fatalf("metric %s%v not registered", name, labels)
	return 0
}

func durableMetricLabelsMatch(metric *dto.Metric, labels []string) bool {
	if len(metric.Label)*2 != len(labels) {
		return false
	}
	for i := 0; i < len(labels); i += 2 {
		found := false
		for _, label := range metric.Label {
			if label.GetName() == labels[i] && label.GetValue() == labels[i+1] {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func durableMetricValue(metric *dto.Metric) float64 {
	switch {
	case metric.Counter != nil:
		return metric.Counter.GetValue()
	case metric.Gauge != nil:
		return metric.Gauge.GetValue()
	default:
		return 0
	}
}
