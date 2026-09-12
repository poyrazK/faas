package daemonunitspec

import "testing"

func TestProductionAsyncExecutionEnabled(t *testing.T) {
	if !hasEnvironment(UnitSchedd(), "FAAS_JOBS_DISPATCH", "1") {
		t.Error("production schedd must dispatch accepted job runs")
	}
	if !hasEnvironment(UnitSchedd(), "FAAS_WORKFLOWS_ENABLED", "1") {
		t.Error("production schedd must dispatch accepted workflow runs")
	}
	if !hasEnvironment(UnitApid(), "FAAS_WORKFLOWS_ENABLED", "1") {
		t.Error("production apid must accept workflow runs when schedd dispatch is enabled")
	}
}
