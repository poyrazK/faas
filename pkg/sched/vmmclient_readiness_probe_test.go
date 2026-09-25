package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppSpecToProtoCarriesReadinessProbe(t *testing.T) {
	const raw = `{"path":"/readyz","period_s":7}`
	got := (AppSpec{ReadinessProbeJSON: raw}).toProto().GetReadinessProbeJson()
	if got != raw {
		t.Fatalf("readiness probe JSON = %q, want %q", got, raw)
	}
}

// adr: 242
func TestAppSpecToProtoCarriesMainDependencies(t *testing.T) {
	want := []api.WorkloadDependency{{Name: "proxy", Condition: api.WorkloadDependencyHealthy}}
	got := (AppSpec{MainDependsOn: want}).toProto().GetMainDependsOn()
	if len(got) != 1 || got[0].GetName() != "proxy" || got[0].GetCondition() != "healthy" {
		t.Fatalf("main_depends_on = %+v, want proxy/healthy", got)
	}
}
