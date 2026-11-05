// adr: 570
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// Each fail-closed traffic control has its own reason, so an operator alert can
// tell a policy-store outage from a revocation outage or node refusal.
func TestTrafficRefusalMetricCountsSealedDecisions(t *testing.T) {
	m := NewMetrics()
	for _, tc := range []struct {
		name     string
		decision trafficDecisionSnapshot
		path     string
		reason   string
		counted  bool
	}{
		{"policy", trafficDecisionSnapshot{path: "public_http", outcome: "refused", refusal: "policy_unavailable"}, "public_http", "policy_unavailable", true},
		{"revocation", trafficDecisionSnapshot{path: "managed_service", outcome: "refused", refusal: "security_unavailable"}, "managed_service", "security_unavailable", true},
		{"deadline", trafficDecisionSnapshot{path: "public_http", outcome: "deadline", refusal: "deadline"}, "public_http", "deadline", true},
		{"phase response", trafficDecisionSnapshot{path: "public_http", outcome: "refused", refusal: "wake_response"}, "public_http", "wake_response", true},
		{"unknown reason", trafficDecisionSnapshot{path: "public_http", outcome: "refused", refusal: "invented"}, "public_http", "other", true},
		{"served", trafficDecisionSnapshot{path: "public_http", outcome: "upstream_response"}, "public_http", "", false},
		{"canceled", trafficDecisionSnapshot{path: "public_http", outcome: "canceled"}, "public_http", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.CollectAndCount(m.trafficRefusals)
			m.observeTrafficDecision(tc.decision)
			if !tc.counted {
				if after := testutil.CollectAndCount(m.trafficRefusals); after != before {
					t.Fatalf("uncounted outcome created a series: %d -> %d", before, after)
				}
				return
			}
			if got := testutil.ToFloat64(m.trafficRefusals.WithLabelValues(tc.path, tc.reason)); got != 1 {
				t.Fatalf("refusals{%s,%s} = %v, want 1", tc.path, tc.reason, got)
			}
		})
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `gateway_traffic_refusals_total{path="public_http",reason="policy_unavailable"} 1`) {
		t.Fatalf("refusal counter is not exposed:\n%s", rec.Body.String())
	}
	var nilMetrics *Metrics
	nilMetrics.observeTrafficDecision(trafficDecisionSnapshot{path: "public_http", outcome: "refused", refusal: "policy_unavailable"})
}

func TestNodeAdmissionRefusalRecordsTrafficReason(t *testing.T) {
	for code, reason := range map[string]string{api.CodeConcurrencyThrottled: "node_capacity", api.CodeHTTPAdmissionUnavailable: "node_admission_unavailable"} {
		t.Run(code, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.test/work", nil)
			r = r.WithContext(withTrafficDecision(context.Background(), false))
			err := grpcerr.ToStatus(api.NewProblem(api.StatusForCode(code), code, "Refused", "Before guest execution"))
			if !writeNodeAdmissionRefusal(httptest.NewRecorder(), r, err) {
				t.Fatal("unrecognized node refusal")
			}
			decision := trafficDecisionEvidence(r.Context(), api.StatusForCode(code), true)
			if decision.outcome != "refused" || decision.refusal != reason {
				t.Fatalf("decision = %+v, want refused/%s", decision, reason)
			}
		})
	}
}
