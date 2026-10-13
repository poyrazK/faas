// adr: 1227 — record bounded, namespace-scoped per-request dependency attempts.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
	"go.opentelemetry.io/otel/trace"
)

func TestServiceProxyObservesOnlyEligibleScopedRequestAttempts(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enabled, expired bool
	}{{name: "strict", enabled: true}, {name: "ordinary"}, {name: "expired", enabled: true, expired: true}} {
		t.Run(tc.name, func(t *testing.T) {
			rule := chaos.Rule{From: "app", To: "dependency", Kind: chaos.KindHTTPStatus, StatusCode: 503, Percent: 100}
			if tc.enabled {
				rule.CorrelationScope = strings.Repeat("b", 16)
			}
			observed := 0
			run := strings.Repeat("a", 32)
			requestID := chaos.RequestCorrelationID(run, rule.CorrelationScope, 1)
			proxy := NewServiceProxy(ServiceProxyConfig{ResolveChaos: func(context.Context, string, string, string) (chaos.Lease, error) {
				expires := time.Now().Add(time.Minute)
				if tc.expired {
					expires = time.Now().Add(-time.Second)
				}
				return chaos.Lease{Generation: "generation", ExpiresAt: expires, Rules: []chaos.Rule{rule}}, nil
			}, ObserveRequestAttempt: func(gotRun, caller, generation string, gotRule chaos.Rule, gotID string) {
				observed++
				if gotRun != run || caller != "caller" || generation != "generation" || gotID != requestID || gotRule.CorrelationScope != rule.CorrelationScope {
					t.Error("scope changed")
				}
			}})
			request := httptest.NewRequest(http.MethodGet, "http://dependency/query", nil)
			request.Header.Set(chaos.RequestCorrelationHeader, requestID)
			response := httptest.NewRecorder()
			proxy.applyScenarioChaos(response, request, trace.SpanFromContext(request.Context()), run, "caller", "dependency")
			want := 0
			if tc.enabled && !tc.expired {
				want = 1
			}
			if observed != want {
				t.Fatalf("observed %d, want %d", observed, want)
			}
		})
	}
}
