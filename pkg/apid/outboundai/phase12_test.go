package outboundai

// adr: 1434 — routes are created and replaced with a strategy, weights and
// a cost route's latency target; invalid combinations are refused; and a
// route's targets' window counts are readable.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRouteStrategyEndpoints(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	ctx := context.Background()
	first, second := aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI), aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI)
	targets := func(extra ...string) string {
		return `"targets":[{"integration_id":"` + first + `"` + extra[0] + `},{"integration_id":"` + second + `"` + extra[1] + `}]`
	}
	for _, body := range []string{
		`{"name":"a",` + targets("", "") + `,"strategy":"weighted"}`,
		`{"name":"a",` + targets(`,"weight":0`, `,"weight":0`) + `,"strategy":"weighted"}`,
		`{"name":"a",` + targets(`,"weight":101`, `,"weight":0`) + `,"strategy":"weighted"}`,
		`{"name":"a",` + targets(`,"weight":50`, `,"weight":50`) + `}`,
		`{"name":"a",` + targets("", "") + `,"strategy":"latency","max_ttfb_ms":500}`,
		`{"name":"a",` + targets("", "") + `,"strategy":"cost","max_ttfb_ms":0}`,
		`{"name":"a",` + targets("", "") + `,"strategy":"random"}`,
	} {
		if w := serveRoute(service.CreateRoute, acct, http.MethodPost, "", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	w := serveRoute(service.CreateRoute, acct, http.MethodPost, "", `{"name":"plain",`+targets("", "")+`}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"strategy":"ordered"`) ||
		!strings.Contains(w.Body.String(), `"max_ttfb_ms":null`) {
		t.Fatalf("a route without a strategy = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.CreateRoute, acct, http.MethodPost, "", `{"name":"split",`+targets(`,"weight":70`, `,"weight":30`)+`,"strategy":"weighted"}`)
	var route api.OutboundAIRoute
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &route) != nil || route.Strategy != api.OutboundAIRouteStrategyWeighted ||
		route.Targets[0].Weight == nil || *route.Targets[0].Weight != 70 || *route.Targets[1].Weight != 30 {
		t.Fatalf("a weighted route = %d %s", w.Code, w.Body.String())
	}
	if w := serveRoute(service.PutRouteTargets, acct, http.MethodPut, route.ID, `{`+targets("", "")+`,"strategy":"cost","max_ttfb_ms":800}`); w.Code != http.StatusNoContent {
		t.Fatalf("PUT a cost route = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.GetRoute, acct, http.MethodGet, route.ID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"strategy":"cost","max_ttfb_ms":800`) || strings.Contains(w.Body.String(), `"weight"`) {
		t.Fatalf("GET a cost route = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.GetRouteStats, acct, http.MethodGet, route.ID, "")
	var stats api.OutboundAIRouteStats
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &stats) != nil || stats.Strategy != api.OutboundAIRouteStrategyCost ||
		len(stats.Targets) != 2 || stats.Targets[1].Target != 2 || stats.Targets[0].MeanCostMillicents != nil ||
		!strings.Contains(w.Body.String(), `"mean_cost_millicents":null`) {
		t.Fatalf("GET stats = %d %s", w.Code, w.Body.String())
	}
	if w := serveRoute(service.GetRouteStats, acct, http.MethodGet, "not-a-uuid", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("stats for a bad route ID = %d", w.Code)
	}
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serveRoute(service.GetRouteStats, other, http.MethodGet, route.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account's stats = %d", w.Code)
	}
}
