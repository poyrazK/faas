package apidserver_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestStatusMetricsExposeBoundedEvaluationRollupAndMutationSignals(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := apidserver.NewStatusMetrics(registry, "apid")
	metrics.ObserveEvaluation("fresh")
	metrics.ObserveEvaluation("unexpected")
	metrics.MarkRollupSuccess(time.Unix(1_789_000_000, 0))
	metrics.ObserveMutation("incident", "create", "ok")
	metrics.ObserveMutation("invented", "invented", "invented")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"apid_status_evaluations_total":                     false,
		"apid_status_rollup_last_success_timestamp_seconds": false,
		"apid_status_incident_mutations_total":              false,
	}
	for _, family := range families {
		if _, ok := wanted[family.GetName()]; ok {
			wanted[family.GetName()] = true
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("metric %s was not registered", name)
		}
	}
}
