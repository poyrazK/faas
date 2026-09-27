package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const (
	circuitBreakerDrillCanaryToken = "canary-drill-service-token-00000000000000000000"
	circuitBreakerDrillActionToken = "action-drill-service-token-00000000000000000000"
)

type circuitBreakerDrillStore struct {
	row         canary.CanaryRow
	observation canary.CircuitBreakerObservation
	err         error
}

func (s *circuitBreakerDrillStore) ListCanaryInFlight(context.Context) ([]canary.CanaryRow, error) {
	return []canary.CanaryRow{s.row}, nil
}

func (s *circuitBreakerDrillStore) CircuitBreakerObservation(context.Context, canary.CanaryRow, time.Time, time.Time) (canary.CircuitBreakerObservation, error) {
	return s.observation, s.err
}

// TestCircuitBreakerFaultDrill exercises the whole automatic decision path
// without live customer traffic: production canary policy calls the real
// authenticated APID internal route, and APID applies the transition through
// MemStore's atomic rollout-recovery implementation. Each subtest starts from
// a real 1/99 canary split and checks the post-drill state and traffic sum.
func TestCircuitBreakerFaultDrill(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*canary.CircuitBreakerObservation)
		observationErr error
		wantAction     canary.CircuitBreakerAction
		wantEvent      string
		wantReason     string
	}{
		{
			name:       "5xx regression aborts exact candidate",
			mutate:     func(o *canary.CircuitBreakerObservation) { o.Candidate.ServerErrors = 10 },
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_5xx",
			wantReason: "5xx error rate regression",
		},
		{
			name: "overall p95 regression aborts exact candidate",
			mutate: func(o *canary.CircuitBreakerObservation) {
				o.Candidate.P95LatencyMS = 250
			},
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_p95_latency",
			wantReason: "p95 latency regression",
		},
		{
			name: "cold-boot request p95 regression aborts exact candidate",
			mutate: func(o *canary.CircuitBreakerObservation) {
				o.Candidate.ColdBootP95LatencyMS = 1800
			},
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_cold_boot_p95",
			wantReason: "cold-boot request latency regression",
		},
		{
			name: "CPU per request regression aborts exact candidate",
			mutate: func(o *canary.CircuitBreakerObservation) {
				o.Candidate.CPUUsec = 80_000_000
			},
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_cpu_per_request",
			wantReason: "CPU per request regression",
		},
		{
			name: "managed dependency regression aborts exact candidate",
			mutate: func(o *canary.CircuitBreakerObservation) {
				o.Candidate.DependencyErrors = 5
			},
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_dependency_errors",
			wantReason: "managed dependency error regression",
		},
		{
			name:       "OOM aborts exact candidate",
			mutate:     func(o *canary.CircuitBreakerObservation) { o.OOMKills = 1 },
			wantAction: canary.CircuitBreakerAbort,
			wantEvent:  "abort_oom",
			wantReason: "workload OOM kill detected",
		},
		{
			name: "low traffic holds candidate",
			mutate: func(o *canary.CircuitBreakerObservation) {
				o.Candidate.Requests = canary.CircuitBreakerMinRequests - 1
			},
			wantAction: canary.CircuitBreakerHold,
			wantEvent:  "hold_insufficient_samples",
		},
		{
			name:       "missing OOM signal holds candidate",
			mutate:     func(o *canary.CircuitBreakerObservation) { o.OOMSignalAvailable = false },
			wantAction: canary.CircuitBreakerHold,
			wantEvent:  "hold_signal_unavailable",
		},
		{
			name:       "missing CPU signal holds candidate",
			mutate:     func(o *canary.CircuitBreakerObservation) { o.CPURequestSignalAvailable = false },
			wantAction: canary.CircuitBreakerHold,
			wantEvent:  "hold_signal_unavailable",
		},
		{
			name:       "missing dependency signal holds candidate",
			mutate:     func(o *canary.CircuitBreakerObservation) { o.DependencySignalAvailable = false },
			wantAction: canary.CircuitBreakerHold,
			wantEvent:  "hold_signal_unavailable",
		},
		{
			name:           "observation outage holds candidate",
			observationErr: context.DeadlineExceeded,
			wantAction:     canary.CircuitBreakerHold,
			wantEvent:      "hold_observation_unavailable",
		},
		{
			name:       "healthy candidate advances through APID",
			wantAction: canary.CircuitBreakerAdvance,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			ctx := context.Background()
			app, err := e.store.CreateApp(ctx, state.App{
				AccountID: e.acct.ID,
				Slug:      "cb-drill-" + string(rune('a'+i)),
				RAMMB:     256,
				Status:    state.AppActive,
			})
			if err != nil {
				t.Fatal(err)
			}
			stable, err := e.store.CreateDeployment(ctx, state.Deployment{
				AppID:       app.ID,
				ImageDigest: "sha256:drill-stable",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkDeploymentLive(ctx, stable.ID); err != nil {
				t.Fatal(err)
			}
			candidate, err := e.store.CreateDeployment(ctx, state.Deployment{
				AppID:            app.ID,
				ImageDigest:      "sha256:drill-candidate",
				CanaryPreset:     "balanced",
				CanaryStep:       0,
				CanaryTotalSteps: 4,
				RolloutState:     "pending",
				TrafficPercent:   1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}

			now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
			observation := canary.CircuitBreakerObservation{
				Candidate: canary.HealthWindow{
					Requests: 100, P95LatencyMS: 100,
					ColdBootRequests: 10, ColdBootP95LatencyMS: 800,
					CPURequests: 100, CPUUsec: 10_000_000,
					DependencyCalls: 10,
				},
				Stable: canary.HealthWindow{
					Requests: 100, P95LatencyMS: 100,
					ColdBootRequests: 10, ColdBootP95LatencyMS: 800,
					CPURequests: 100, CPUUsec: 10_000_000,
					DependencyCalls: 10,
				},
				StableDeploymentID:        stable.ID,
				HasStable:                 true,
				OOMSignalAvailable:        true,
				CPURequestSignalAvailable: true,
				DependencySignalAvailable: true,
			}
			if tc.mutate != nil {
				tc.mutate(&observation)
			}
			drillStore := &circuitBreakerDrillStore{
				row: canary.CanaryRow{
					ID:                candidate.ID,
					AppID:             app.ID,
					AppSlug:           app.Slug,
					Scope:             candidate.Scope,
					CanaryPreset:      candidate.CanaryPreset,
					CanaryStep:        candidate.CanaryStep,
					CanaryTotalSteps:  candidate.CanaryTotalSteps,
					CanaryStepStarted: now.Add(-time.Hour),
					RolloutStarted:    now.Add(-time.Hour),
					RolloutState:      "rolling_out",
				},
				observation: observation,
				err:         tc.observationErr,
			}

			mux := http.NewServeMux()
			if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", circuitBreakerDrillCanaryToken, circuitBreakerDrillActionToken); err != nil {
				t.Fatal(err)
			}
			internalServer := httptest.NewServer(mux)
			defer internalServer.Close()
			client := api.NewInternalSafeDeployClient(internalServer.URL, circuitBreakerDrillCanaryToken, circuitBreakerDrillActionToken)
			ops := wire.NewOpsMetrics("apid_circuit_breaker_drill")
			progression := canary.NewProgression(drillStore, client, ops, slog.New(slog.NewTextHandler(io.Discard, nil)))
			progression.Now = func() time.Time { return now }

			stats, err := progression.Once(ctx)
			if err != nil {
				t.Fatalf("run circuit breaker progression: %v", err)
			}
			switch tc.wantAction {
			case canary.CircuitBreakerAbort:
				if stats.CircuitBreakerAborted != 1 || stats.Advanced != 0 || stats.Errors != 0 {
					t.Fatalf("abort drill stats = %+v; want one abort, no advance/error", stats)
				}
				gotCandidate, err := e.store.DeploymentByID(ctx, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				gotStable, err := e.store.DeploymentByID(ctx, stable.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotCandidate.RolloutState != "aborted" || gotCandidate.TrafficPercent != 0 || !strings.Contains(gotCandidate.RolloutAbortedReason, tc.wantReason) {
					t.Fatalf("candidate after abort = state:%q traffic:%d reason:%q", gotCandidate.RolloutState, gotCandidate.TrafficPercent, gotCandidate.RolloutAbortedReason)
				}
				if gotStable.TrafficPercent != 100 {
					t.Fatalf("predecessor traffic after abort = %d, want 100", gotStable.TrafficPercent)
				}
				audits, err := e.store.ListDeploymentAudit(ctx, candidate.ID, 10)
				if err != nil || len(audits) != 1 || audits[0].Kind != state.DeployRolledBack {
					t.Fatalf("candidate rollback audits = %+v, err=%v; want one deploy.rolled_back audit", audits, err)
				}
			case canary.CircuitBreakerHold:
				if stats.SkippedCircuitBreaker != 1 || stats.CircuitBreakerAborted != 0 || stats.Advanced != 0 {
					t.Fatalf("hold drill stats = %+v; want held with no advance/abort", stats)
				}
				gotCandidate, err := e.store.DeploymentByID(ctx, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				gotStable, err := e.store.DeploymentByID(ctx, stable.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotCandidate.RolloutState != "rolling_out" || gotCandidate.TrafficPercent != 1 || gotStable.TrafficPercent != 99 {
					t.Fatalf("held rollout changed state/traffic: candidate=%+v stable=%+v", gotCandidate, gotStable)
				}
			case canary.CircuitBreakerAdvance:
				if stats.Advanced != 1 || stats.CircuitBreakerAborted != 0 || stats.Errors != 0 {
					t.Fatalf("healthy drill stats = %+v; want one advance and no abort/error", stats)
				}
				gotCandidate, err := e.store.DeploymentByID(ctx, candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				gotStable, err := e.store.DeploymentByID(ctx, stable.ID)
				if err != nil {
					t.Fatal(err)
				}
				if gotCandidate.CanaryStep != 1 || gotCandidate.TrafficPercent != 10 || gotStable.TrafficPercent != 90 {
					t.Fatalf("healthy progression state/traffic: candidate=%+v stable=%+v", gotCandidate, gotStable)
				}
			}
			if tc.wantEvent != "" {
				if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal(tc.wantEvent)); got != 1 {
					t.Fatalf("event %q counter = %g, want 1", tc.wantEvent, got)
				}
			}
			live, err := e.store.LiveDeployments(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			trafficSum := 0
			for _, deployment := range live {
				if deployment.Scope == candidate.Scope {
					trafficSum += deployment.TrafficPercent
				}
			}
			if trafficSum != 100 {
				t.Fatalf("live traffic sum after %s = %d, want 100", tc.name, trafficSum)
			}
		})
	}
}
