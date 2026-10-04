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
	dto "github.com/prometheus/client_model/go"
)

type exactDeploymentTimingBackend struct {
	*deploymentSmokeRoutingBackend
	admitTrace *wakePhaseTrace
}

func (b *exactDeploymentTimingBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	b.admitTrace = wakePhaseTraceFrom(ctx)
	return (&phaseTraceBackend{fakeBackend: b.fakeBackend}).Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
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
				wantCount := uint64(0)
				if state == "cold" {
					wantCount = 1
				}
				if got := metric.GetHistogram().GetSampleCount(); got != wantCount {
					t.Fatalf("platform wake observations=%d, want %d", got, wantCount)
				}
			})
		}
	}
}
