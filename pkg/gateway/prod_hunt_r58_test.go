package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r58: a release's 5xx series was created by its first error, so
// increase() dropped the errors counted before Prometheus first scraped it
// and app health reported 10 of 41 failed while 30 of 62 had. Admitting a
// deployment now creates its four status-class series at zero.
func TestAdmittedDeploymentHasEveryStatusClassSeries(t *testing.T) {
	m := NewMetrics()
	if got := m.deploymentLabels.admit("app-1", api.PlanPro, "dep-1"); got != "dep-1" {
		t.Fatalf("admit = %q, want dep-1", got)
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]uint64{}
	for _, family := range families {
		if family.GetName() != "gateway_request_duration_by_deployment_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["app"] == "app-1" && labels["deployment"] == "dep-1" {
				classes[labels["class"]] = metric.GetHistogram().GetSampleCount()
			}
		}
	}
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		count, ok := classes[class]
		if !ok || count != 0 {
			t.Fatalf("class %s series = %d (present %t), want a zero series; got %v", class, count, ok, classes)
		}
	}
	// A deployment over the plan cap collapses to __other__ and creates no
	// series of its own.
	free := NewMetrics()
	if got := free.deploymentLabels.admit("app-free", api.PlanFree, "dep-x"); got != otherDeploymentLabel {
		t.Fatalf("free admit = %q, want %q", got, otherDeploymentLabel)
	}
}
