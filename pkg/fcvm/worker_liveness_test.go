// adr: 137 — workers have no HTTP contract, so plan-default liveness skips them.
package fcvm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkerWakeSkipsPlanDefaultHTTPLiveness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		override json.RawMessage
		want     bool
	}{
		{name: "request keeps default probe", mode: api.ExecutionModeRequest, want: true},
		{name: "legacy undeclared keeps default probe", mode: "", want: true},
		{name: "worker skips default probe", mode: api.ExecutionModeWorker, want: false},
		{name: "worker keeps explicit probe", mode: api.ExecutionModeWorker, override: json.RawMessage(`{"path":"/live"}`), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			started := false
			m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{Path: "/healthz", PeriodSeconds: 5, ConsecutiveFailures: 3}).
				WithLivenessProbeStarter(func(context.Context, string, int, string, LivenessProbeConfig) context.CancelFunc {
					started = true
					return func() {}
				})
			req := admissionWake("worker-liveness")
			req.ExecutionMode, req.LivenessProbe = tc.mode, tc.override
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if started != tc.want {
				t.Fatalf("liveness loop started=%v, want %v", started, tc.want)
			}
		})
	}
}

func TestInstanceHasNoHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		inst Instance
		want bool
	}{
		{inst: Instance{ExecutionMode: api.ExecutionModeWorker}, want: true},
		{inst: Instance{ExecutionMode: api.ExecutionModeJob}, want: true},
		{inst: Instance{ExecutionMode: api.ExecutionModeService}},
		{inst: Instance{ExecutionMode: api.ExecutionModeRequest, Characterization: api.CharacterizationReport{ObservedClass: "worker"}}},
		{inst: Instance{Characterization: api.CharacterizationReport{ObservedClass: "worker"}}, want: true},
		{inst: Instance{Characterization: api.CharacterizationReport{ObservedClass: "request"}}},
	} {
		if got := instanceHasNoHTTPContract(&tc.inst); got != tc.want {
			t.Fatalf("instanceHasNoHTTPContract(mode=%q class=%q) = %v, want %v",
				tc.inst.ExecutionMode, tc.inst.Characterization.ObservedClass, got, tc.want)
		}
	}
}
