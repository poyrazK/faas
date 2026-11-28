package gateway

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r58 follow-up: when the picker learns a new release's instance of
// an app this gateway already serves, the release's 2xx–5xx series exist at
// zero before its first request.
func TestRecordedTargetCreatesItsReleaseSeries(t *testing.T) {
	m := NewMetrics()
	b := NewPGBackend(nil, nil, nil).WithMetrics(m)
	m.deploymentLabels.admit("app-1", api.PlanPro, "dep-old")
	before := testutil.CollectAndCount(m.requestDurationByDeployment)
	b.RecordTarget("app-1", Target{AppID: "app-1", InstanceID: "ins-1", NodeID: "node-1", DeploymentID: "dep-new"})
	if got := testutil.CollectAndCount(m.requestDurationByDeployment) - before; got != 4 {
		t.Fatalf("new series = %d, want 4 (2xx..5xx for dep-new)", got)
	}
	b.RecordTarget("unseen-app", Target{AppID: "unseen-app", InstanceID: "ins-2", NodeID: "node-1", DeploymentID: "dep-x"})
	if got := testutil.CollectAndCount(m.requestDurationByDeployment) - before; got != 4 {
		t.Fatalf("an app this gateway never served got series: %d", got)
	}
}
