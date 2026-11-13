// adr: 137 — workers have no HTTP contract, so plan-default liveness skips them.
package fcvm

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

// ADR-079 amendment 1: plan-default liveness checks that the server answers,
// on the contract startup proved. H10-23: a TCP-readiness nginx answered the
// default /healthz with 404 and was parked. Prod 2026-11-12: Spring apps
// readying on /actuator/health answered /healthz with 404 and were destroyed
// ~10 s after every boot; a gRPC app could not answer the HTTP probe at all.
func TestPlanDefaultLivenessFollowsStartupContract(t *testing.T) {
	type want struct {
		path       string
		grpc       bool
		service    string
		unpromised bool
		period     int
	}
	for _, tc := range []struct {
		name     string
		inst     Instance
		override json.RawMessage
		want     want
	}{
		{name: "tcp readiness: any answer below 500 is alive",
			want: want{path: "/healthz", unpromised: true, period: 5}},
		{name: "raw tcp listener: no default http probe",
			inst: Instance{RoutedPortRawTCP: true},
			want: want{}},
		{name: "raw tcp listener: explicit liveness path still runs",
			inst:     Instance{RoutedPortRawTCP: true},
			override: json.RawMessage(`{"path":"/live"}`),
			want:     want{path: "/live", period: 5}},
		{name: "readiness on /healthz stays strict",
			inst: Instance{HealthcheckPath: "/healthz"},
			want: want{path: "/healthz", period: 5}},
		{name: "declared health path: /healthz is unpromised",
			inst: Instance{HealthcheckPath: "/actuator/health"},
			want: want{path: "/healthz", unpromised: true, period: 5}},
		{name: "timing-only override keeps the startup contract",
			inst:     Instance{HealthcheckPath: "/actuator/health"},
			override: json.RawMessage(`{"interval_s":10}`),
			want:     want{path: "/healthz", unpromised: true, period: 10}},
		{name: "explicit liveness path is strict",
			inst:     Instance{HealthcheckPath: "/actuator/health"},
			override: json.RawMessage(`{"path":"/live"}`),
			want:     want{path: "/live", period: 5}},
		{name: "grpc readiness: grpc liveness on the same service",
			inst: Instance{HealthcheckGRPC: true, HealthcheckGRPCService: "catalog.v1.Catalog", Plan: api.PlanPro},
			want: want{grpc: true, service: "catalog.v1.Catalog", period: 5}},
		{name: "grpc readiness without grpc liveness: no default probe",
			inst: Instance{HealthcheckGRPC: true, Plan: api.PlanHobby},
			want: want{}},
		{name: "grpc readiness: explicit http liveness wins",
			inst:     Instance{HealthcheckGRPC: true, Plan: api.PlanHobby},
			override: json.RawMessage(`{"path":"/live"}`),
			want:     want{path: "/live", period: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			var got LivenessProbeConfig
			m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{Path: "/healthz", PeriodSeconds: 5, ConsecutiveFailures: 3}).
				WithLivenessProbeStarter(func(_ context.Context, _ string, _ int, _ string, cfg LivenessProbeConfig) context.CancelFunc {
					got = cfg
					return func() {}
				})
			inst := tc.inst
			inst.ExecutionMode = api.ExecutionModeRequest
			m.mu.Lock()
			m.live["web"] = &inst
			m.mu.Unlock()
			m.startLivenessLoop(t.Context(), "web", 1, tc.override)
			gotWant := want{path: got.Path, grpc: got.GRPC, service: got.GRPCService, unpromised: got.UnpromisedPath, period: got.PeriodSeconds}
			if gotWant != tc.want {
				t.Fatalf("liveness config = %+v, want %+v", gotWant, tc.want)
			}
			m.cancelLivenessLoop("web")
		})
	}
}

// The wake carries the gRPC readiness service to the liveness probe.
func TestGRPCWakeLivenessUsesReadinessService(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	var got LivenessProbeConfig
	m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{Path: "/healthz", PeriodSeconds: 5, ConsecutiveFailures: 3}).
		WithLivenessProbeStarter(func(_ context.Context, _ string, _ int, _ string, cfg LivenessProbeConfig) context.CancelFunc {
			got = cfg
			return func() {}
		})
	req := admissionWake("grpc-liveness")
	req.ExecutionMode, req.Plan = api.ExecutionModeRequest, api.PlanScale
	req.HealthcheckGRPC, req.HealthcheckGRPCService = true, "scorer.v1.Scorer"
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if !got.GRPC || got.GRPCService != "scorer.v1.Scorer" || got.Path != "" || got.UnpromisedPath {
		t.Fatalf("liveness config = %+v, want gRPC on scorer.v1.Scorer", got)
	}
}

// The wake's raw TCP mark reaches the instance, the liveness decision and the
// adoption record, so a vmmd restart does not bring the HTTP probe back.
func TestRawTCPWakeSkipsDefaultLivenessAndJournalsIt(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	started := false
	m.WithLivenessProbes(NewLivenessRegistry(), LivenessProbeConfig{Path: "/healthz", PeriodSeconds: 5, ConsecutiveFailures: 3}).
		WithLivenessProbeStarter(func(context.Context, string, int, string, LivenessProbeConfig) context.CancelFunc {
			started = true
			return func() {}
		})
	req := admissionWake("raw-tcp-liveness")
	req.ExecutionMode, req.RoutedPortRawTCP = api.ExecutionModeService, true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if started {
		t.Fatal("plan-default HTTP liveness started for a raw TCP listener")
	}
	m.mu.Lock()
	inst := m.live["raw-tcp-liveness"]
	m.mu.Unlock()
	if inst == nil || !inst.RoutedPortRawTCP {
		t.Fatalf("instance RoutedPortRawTCP not stamped: %+v", inst)
	}
	if block := m.adoptionBlockFor(inst, time.Now()); !block.Instance.RoutedPortRawTCP {
		t.Fatal("adoption record dropped routed_port_raw_tcp")
	}
}
