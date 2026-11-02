// adr: 20261028110044 — route evidence skips platform refusals (amendment 3).
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only a 5xx that a Gregale dependency caused is marked; refusals a candidate
// can cause still count against it.
func TestTelemetryMarksPlatformRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refusal string
		status  int
		want    bool
	}{
		{"node admission unavailable", "node_admission_unavailable", http.StatusServiceUnavailable, true},
		{"traffic policy unavailable", "policy_unavailable", http.StatusServiceUnavailable, true},
		{"rate-limit state unavailable", "rate_limit_unavailable", http.StatusServiceUnavailable, true},
		{"revocation state unavailable", "security_unavailable", http.StatusServiceUnavailable, true},
		{"app request limit", "node_capacity", http.StatusTooManyRequests, false},
		{"no routable deployment", "capacity", http.StatusServiceUnavailable, false},
		{"open circuit", "circuit", http.StatusServiceUnavailable, false},
		{"deadline", "deadline", http.StatusGatewayTimeout, false},
		{"failed wake", "", http.StatusServiceUnavailable, false},
		{"platform reason below 5xx", "policy_unavailable", http.StatusTooManyRequests, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{requestTelemetry: makeTestRecorder()}
			acct, app := uuid.New(), uuid.New()
			r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/items", nil), acct, app)
			r = r.WithContext(withTrafficDecision(r.Context(), false))
			if tc.refusal != "" {
				recordTrafficRefusal(r.Context(), tc.refusal)
			}
			h.observe(r, tc.status, app.String(), string(api.PlanPro), false, Target{DeploymentID: uuid.NewString()})
			rows := h.requestTelemetry.DrainBatch(4)
			if len(rows) != 1 || rows[0].PlatformRefusal != tc.want {
				t.Fatalf("rows %+v, want platform_refusal %v", rows, tc.want)
			}
		})
	}
}

func TestCollapseKeepsPlatformRefusalsApart(t *testing.T) {
	base := RequestTelemetryRow{AccountID: uuid.New(), AppID: uuid.New(), Route: "GET /items", Method: "GET", Status: http.StatusServiceUnavailable, ReceivedAt: time.Unix(1_800_000_000, 0), Count: 1}
	refused := base
	refused.PlatformRefusal = true
	out := collapseRequestTelemetry([]RequestTelemetryRow{base, refused, refused})
	if len(out) != 2 || out[0].PlatformRefusal || out[0].Count != 1 || !out[1].PlatformRefusal || out[1].Count != 2 {
		t.Fatalf("collapsed = %+v", out)
	}
}
