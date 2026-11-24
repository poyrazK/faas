// spec: §12
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/sched"
	dto "github.com/prometheus/client_model/go"
)

type exactDeploymentTimingBackend struct {
	*deploymentSmokeRoutingBackend
	admitTrace *wakePhaseTrace
	// startupOOM reports, like the schedd client does for a response carrying
	// startup_oom, that the admitted instance ran out of memory while booting.
	startupOOM bool
}

func (b *exactDeploymentTimingBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	b.admitTrace = wakePhaseTraceFrom(ctx)
	wakeID, method, atCapacity, err := (&phaseTraceBackend{fakeBackend: b.fakeBackend}).Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
	if err == nil && !atCapacity && b.startupOOM {
		b.mu.Lock()
		instance := b.targets[len(b.targets)-1].InstanceID
		b.mu.Unlock()
		sched.NoteStartupOOM(ctx, instance)
	}
	return wakeID, method, atCapacity, err
}

// TestDeploymentSmokeStartupOOMCountsAsStartupOOM: a candidate that runs out
// of memory on the boot its smoke admitted is counted under startup_oom, not
// deployment_smoke, so a release that grows guest overhead still moves the
// startup_oom exclusion rate ADR-20261123170000 treats as a platform signal.
func TestDeploymentSmokeStartupOOMCountsAsStartupOOM(t *testing.T) {
	fake := &fakeBackend{
		app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree, MaxConcurrency: 1},
		host: "demo.apps.dom", upstream: "node-1",
	}
	backend := &exactDeploymentTimingBackend{
		deploymentSmokeRoutingBackend: &deploymentSmokeRoutingBackend{fakeBackend: fake, deploymentID: "dep-candidate", token: "test-challenge"},
		startupOOM:                    true,
	}
	metrics := NewMetrics()
	h := NewHandlerWith(backend, metrics, nil)
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recordForwardedFirstByte(r.Context())
			w.WriteHeader(http.StatusNoContent)
		})
	})
	req := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/", nil)
	req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
	req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, backend.deploymentID)
	req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, backend.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for prefix, want := range map[string]int{
		"gateway_platform_wake_latency_seconds_count ":                     0,
		`gateway_platform_wake_excluded_total{reason="startup_oom"} `:      1,
		`gateway_platform_wake_excluded_total{reason="deployment_smoke"} `: 0,
	} {
		if got := metricLineValue(t, metrics, prefix); got != want {
			t.Errorf("%s= %d, want %d", prefix, got, want)
		}
	}
}

func TestExactDeploymentWakeRecordsPlatformTiming(t *testing.T) {
	for _, entry := range []string{"preview", "smoke"} {
		for _, state := range []string{"cold", "warm", "failed", "at_capacity"} {
			t.Run(entry+"/"+state, func(t *testing.T) {
				fake := &fakeBackend{
					app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree, MaxConcurrency: 1},
					host: "demo.apps.dom", upstream: "node-1",
				}
				if entry == "preview" {
					fake.app.PinnedDeploymentID = "dep-candidate"
				}
				switch state {
				case "warm":
					fake.AddTarget(Target{NodeID: "node-1", InstanceID: "candidate-1", DeploymentID: "dep-candidate", AddedAt: time.Now()})
				case "failed":
					fake.wakeErr = errors.New("test admission failure")
				case "at_capacity":
					fake.atCapForCalls = 1
				}
				backend := &exactDeploymentTimingBackend{deploymentSmokeRoutingBackend: &deploymentSmokeRoutingBackend{
					fakeBackend: fake, deploymentID: "dep-candidate", token: "test-challenge",
				}}
				metrics := NewMetrics()
				h := NewHandlerWith(backend, metrics, nil)
				forwarded := false
				h.WithForwarding(func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						forwarded = true
						trace := wakePhaseTraceFrom(r.Context())
						if state == "cold" {
							if trace == nil || trace != backend.admitTrace {
								t.Error("admission and first-byte forwarding did not share a platform trace")
							} else if phases := trace.gatewayPhasesMS(time.Now()); len(phases) != 5 {
								t.Errorf("incomplete gateway phases: %#v", phases)
							}
						} else if trace != nil {
							t.Error("warm exact-deployment request carried a wake trace")
						}
						recordForwardedFirstByte(r.Context())
						w.WriteHeader(http.StatusNoContent)
					})
				})
				req := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/", nil)
				if entry == "smoke" {
					req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
					req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, backend.deploymentID)
					req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, backend.token)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				wantForwarded := state == "cold" || state == "warm"
				if forwarded != wantForwarded || (wantForwarded && rec.Code != http.StatusNoContent) {
					t.Fatalf("forwarded=%v status=%d body=%s", forwarded, rec.Code, rec.Body.String())
				}
				var metric dto.Metric
				if err := metrics.platformWakeLatency.Write(&metric); err != nil {
					t.Fatal(err)
				}
				// A cold preview is a platform wake. A cold smoke still records
				// its phase trace but stays out of the platform SLO histogram:
				// it waits for its candidate's first-boot snapshot, which is
				// deploy-pipeline time (counted as excluded instead).
				wantCount, wantExcluded := uint64(0), 0
				if state == "cold" && entry == "preview" {
					wantCount = 1
				}
				if state == "cold" && entry == "smoke" {
					wantExcluded = 1
				}
				if got := metric.GetHistogram().GetSampleCount(); got != wantCount {
					t.Fatalf("platform wake observations=%d, want %d", got, wantCount)
				}
				if got := metricLineValue(t, metrics, `gateway_platform_wake_excluded_total{reason="deployment_smoke"} `); got != wantExcluded {
					t.Fatalf("deployment smoke exclusions=%d, want %d", got, wantExcluded)
				}
			})
		}
	}
}
