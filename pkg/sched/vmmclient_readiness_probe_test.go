// adr: 281 — pass the configured primary-app readiness probe to the VM.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestAppSpecToProtoCarriesReadinessProbe(t *testing.T) {
	const raw = `{"path":"/readyz","period_s":7}`
	got := (sched.AppSpec{ReadinessProbeJSON: raw}).ToProto().GetReadinessProbeJson()
	if got != raw {
		t.Fatalf("readiness probe JSON = %q, want %q", got, raw)
	}
}

// adr: 242
func TestAppSpecToProtoCarriesMainDependencies(t *testing.T) {
	want := []api.WorkloadDependency{{Name: "proxy", Condition: api.WorkloadDependencyHealthy}}
	got := (sched.AppSpec{MainDependsOn: want}).ToProto().GetMainDependsOn()
	if len(got) != 1 || got[0].GetName() != "proxy" || got[0].GetCondition() != "healthy" {
		t.Fatalf("main_depends_on = %+v, want proxy/healthy", got)
	}
}
