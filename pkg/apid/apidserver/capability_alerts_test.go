// adr: 20261023115741 — a feature behind a narrower capability than the route
// it rides on is refused per account, so an operator switch turned on to test
// one account does not open it to every account.
package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func TestAlertMetricsFollowTheirCapability(t *testing.T) {
	tests := []struct {
		capability string
		rule       api.CreateAlertRuleRequest
	}{
		{"log-search", api.CreateAlertRuleRequest{Name: "log-errors", Metric: api.AlertRuleMetricLogSearchMatches, LogQuery: "level=error", Comparison: "gt", Threshold: 1, WindowSpec: "15m", WebhookURL: "https://hooks.example.com/a", WebhookSecret: "shh"}},
		{"custom-metric-history", api.CreateAlertRuleRequest{Name: "orders-pending", Metric: api.AlertRuleMetricCustomMetric, CustomMetricName: "orders_pending", Comparison: "gt", Threshold: 1, WindowSpec: "15m", WebhookURL: "https://hooks.example.com/a", WebhookSecret: "shh"}},
	}
	for _, tc := range tests {
		t.Run(tc.capability, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			srv := *e.S()
			if err := srv.InstallCapabilityGate(); err != nil {
				t.Fatal(err)
			}
			if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "alerts-api", RAMMB: 256}, nil); r.Code != http.StatusCreated {
				t.Fatalf("create app: %d %s", r.Code, r.Body)
			}
			if r := e.Do(t, http.MethodPost, "/v1/apps/alerts-api/alerts", tc.rule, nil); !capabilityRefused(r) {
				t.Fatalf("%s alert with its capability off = %d %s, want capability_not_released", tc.rule.Metric, r.Code, r.Body)
			}
			srv.SeedPlatformFlag(capabilityFlag(t, tc.capability), platformflags.Definition{Enabled: true, Default: true})
			if r := e.Do(t, http.MethodPost, "/v1/apps/alerts-api/alerts", tc.rule, nil); capabilityRefused(r) {
				t.Fatalf("%s alert with its capability on was refused by the gate: %s", tc.rule.Metric, r.Body)
			}
		})
	}
}

func TestCustomMetricHistoryRoutesFollowTheirCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "metrics-api", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/metrics-api/custom-metrics", nil, nil); missingRoute(r) {
		t.Fatalf("custom metrics (beta) must stay served: %d %s", r.Code, r.Body)
	}
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/v1/apps/metrics-api/custom-metrics/orders_pending/series"},
		{http.MethodPost, "/v1/apps/metrics-api/otlp/v1/metrics"},
	} {
		if r := e.Do(t, req.method, req.path, nil, nil); !missingRoute(r) {
			t.Errorf("%s %s with history off = %d %s, want the missing-route answer", req.method, req.path, r.Code, r.Body)
		}
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "custom-metric-history"), platformflags.Definition{Enabled: true, Default: true})
	if r := e.Do(t, http.MethodGet, "/v1/apps/metrics-api/custom-metrics/orders_pending/series", nil, nil); missingRoute(r) {
		t.Fatalf("history series with its capability on = %d %s", r.Code, r.Body)
	}
}

func TestRealtimeTriggersInTheManifestFollowRealtime(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	manifest := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindRealtime}}}
	if prob := srv.CheckManifestSettings(*e.Acct(), manifest); prob == nil || prob.Code != api.CodeCapabilityNotReleased {
		t.Fatalf("a realtime trigger with realtime off = %+v, want capability_not_released", prob)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "realtime"), platformflags.Definition{Enabled: true, Default: true})
	if prob := srv.CheckManifestSettings(*e.Acct(), manifest); prob != nil {
		t.Fatalf("a realtime trigger with realtime on = %+v", prob)
	}
}
