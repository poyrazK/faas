// adr: 20261121231500
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// A route probe is the platform's request: it reaches the instance but is
// not counted as the customer's request or response bytes in usage_minutes.
func TestRouteProbeNotCountedAsCustomerUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe bool
	}{{"customer request", false}, {"route probe", true}} {
		t.Run(tc.name, func(t *testing.T) {
			app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, SessionAffinity: true}
			target := Target{AppID: app.ID, DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), NodeID: "node-1"}
			backend := &retryCompletionBackend{fakeBackend: &fakeBackend{app: app, host: "app.test"}}
			backend.AddTarget(target)
			h := NewHandlerWith(backend, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
				WithRetryEnabled(false).WithRetryDefault(RetryPolicy{MaxAttempts: 2})
			h.lastSeen = NewMemoryLastSeen()
			h.egressSink = egresssink.NewEgressSink()
			h.requestTelemetry = makeTestRecorder()
			forwarded := 0
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					forwarded++
					_, _ = w.Write([]byte("response"))
				})
			})
			req := httptest.NewRequest(http.MethodGet, "http://app.test/work", nil)
			if tc.probe {
				req = req.WithContext(withRouteProbe(req.Context(), target.DeploymentID, "probe-token", false))
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != http.StatusOK || forwarded != 1 {
				t.Fatalf("status = %d forwarded = %d body=%s", response.Code, forwarded, response.Body)
			}
			records := h.egressSink.DrainRecords()
			if tc.probe {
				if len(records) != 0 {
					t.Fatalf("probe usage = %+v, want none", records)
				}
				return
			}
			if len(records) != 1 || records[0].Requests != 1 || records[0].Bytes != uint64(len("response")) {
				t.Fatalf("customer usage = %+v, want one request and its bytes", records)
			}
		})
	}
}
