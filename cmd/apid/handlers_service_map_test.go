package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

func serviceMapEnv(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	e := setup(t, plan)
	e.s.serviceMapEnabled = true
	return e
}

func decodeServiceMap(t *testing.T, rec *httptest.ResponseRecorder) api.ServiceMapResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.ServiceMapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestGetServiceMap_DisabledReturns503(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodGet, "/v1/service-map", nil, nil)
	assertProblem(t, rec, http.StatusServiceUnavailable, "service_map_unavailable")
}

func TestGetServiceMap_FreePlanReturns402(t *testing.T) {
	e := serviceMapEnv(t, api.PlanFree)
	rec := e.do(t, http.MethodGet, "/v1/service-map", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPerAppMetricsNotAllowed)
}

func TestGetServiceMap_InvalidRange(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	rec := e.do(t, http.MethodGet, "/v1/service-map?range=99y", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestGetServiceMap_Degraded_NoPrometheus(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	out := decodeServiceMap(t, e.do(t, http.MethodGet, "/v1/service-map", nil, nil))
	if out.Range != api.ServiceMapDefaultRange {
		t.Fatalf("range = %q, want default %q", out.Range, api.ServiceMapDefaultRange)
	}
	if want := appmetrics.SourceDegradedPrefix + "prometheus not configured"; out.Source != want {
		t.Fatalf("source = %q, want %q", out.Source, want)
	}
	if out.Nodes != nil || out.Edges != nil {
		t.Fatalf("degraded map must not carry rows: %+v", out)
	}
}

func TestGetServiceMap_Degraded_QueryFails(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	createApp(t, e, "deg-app")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"parse error"}`)
	}))
	t.Cleanup(srv.Close)
	e.s.WithStatusCache(srv.URL, "")

	out := decodeServiceMap(t, e.do(t, http.MethodGet, "/v1/service-map?range=5m", nil, nil))
	if !strings.HasPrefix(out.Source, appmetrics.SourceDegradedPrefix) {
		t.Fatalf("source = %q, want degraded", out.Source)
	}
	if strings.Contains(out.Source, "parse error") {
		t.Fatalf("source leaked the Prometheus error: %q", out.Source)
	}
	if out.Nodes != nil || out.Edges != nil {
		t.Fatalf("degraded map must not carry rows: %+v", out)
	}
}

// TestGetServiceMap_HappyPath_WithProm is the capability acceptance test
// (pkg/productcap/catalog.json, ADR-740). It pins the closed-set selectors,
// the second ownership boundary on returned labels, ranking, error rate, and
// success-only latency percentiles.
func TestGetServiceMap_HappyPath_WithProm(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	api1 := createApp(t, e, "public-api")
	payments := createApp(t, e, "payments")
	ledger := createApp(t, e, "ledger")
	createApp(t, e, "quiet")
	foreignAccount, _ := mustCreateAccount(t, e.store, "service-map-foreign", api.PlanHobby)
	foreignAppID := mustSeedAppFor(t, e.store, foreignAccount.ID, "foreign-app")

	// The gateway labels edges with dashed UUIDs (uuid.UUID.String), while the
	// store hands out 32-hex IDs; the selectors and fixtures use the gateway
	// form so this pins the translation between the two.
	gw := canonicalServiceMapAppID
	var queries atomic.Int32
	installPromFixture(t, &e, func(query string) string {
		queries.Add(1)
		for _, id := range []string{api1.ID, payments.ID, ledger.ID} {
			if strings.Count(query, gw(id)) != 2 {
				t.Errorf("query must constrain caller and target to owned app %s: %s", gw(id), query)
			}
		}
		if strings.Contains(query, gw(foreignAppID)) || strings.Contains(query, foreignAppID) {
			t.Errorf("query includes foreign app ID: %s", query)
		}
		switch {
		case strings.Contains(query, "gateway_service_dependency_edge_calls_total"):
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"96"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"4"]},`+
				`{"metric":{"caller_app":"%[2]s","target_app":"%[3]s","outcome":"success"},"value":[1,"10"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[3]s","outcome":"success"},"value":[1,"0.2"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[4]s","outcome":"success"},"value":[1,"50"]}]}}`,
				gw(api1.ID), gw(payments.ID), gw(ledger.ID), gw(foreignAppID))
		case strings.Contains(query, "gateway_service_dependency_duration_seconds_bucket"):
			if !strings.Contains(query, `outcome="success"`) {
				t.Errorf("latency query must be success-only: %s", query)
			}
			return fmt.Sprintf(`{"data":{"resultType":"vector","result":[`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"0.01"},"value":[1,"48"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"0.05"},"value":[1,"96"]},`+
				`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","le":"+Inf"},"value":[1,"96"]}]}}`,
				gw(api1.ID), gw(payments.ID))
		default:
			t.Errorf("unexpected query: %s", query)
			return `{"data":{"resultType":"vector","result":[]}}`
		}
	})

	out := decodeServiceMap(t, e.do(t, http.MethodGet, "/v1/service-map", nil, nil))
	if out.Source != appmetrics.SourcePrometheus || out.Range != "1h" || out.Truncated {
		t.Fatalf("envelope = source %q range %q truncated %v", out.Source, out.Range, out.Truncated)
	}
	if got := queries.Load(); got != 2 {
		t.Fatalf("Prometheus queries = %d, want 2", got)
	}
	if len(out.Edges) != 2 {
		t.Fatalf("edges = %+v, want public-api→payments and payments→ledger only", out.Edges)
	}
	top := out.Edges[0]
	if top.CallerAppSlug != "public-api" || top.TargetAppSlug != "payments" || top.Calls != 100 || top.Errors != 4 {
		t.Fatalf("top edge = %+v", top)
	}
	if math.Abs(top.ErrorRatePct-4) > 1e-9 {
		t.Fatalf("error_rate_pct = %v, want 4", top.ErrorRatePct)
	}
	if math.Abs(top.LatencyP50MS-10) > 1e-9 || top.LatencyP95MS <= top.LatencyP50MS {
		t.Fatalf("latency p50/p95 = %v/%v, want 10 and above", top.LatencyP50MS, top.LatencyP95MS)
	}
	if second := out.Edges[1]; second.CallerAppSlug != "payments" || second.TargetAppSlug != "ledger" || second.Calls != 10 {
		t.Fatalf("second edge = %+v", second)
	}
	slugs := make([]string, 0, len(out.Nodes))
	for _, node := range out.Nodes {
		slugs = append(slugs, node.AppSlug)
	}
	if got := strings.Join(slugs, ","); got != "ledger,payments,public-api" {
		t.Fatalf("nodes = %s, want ledger,payments,public-api", got)
	}
}

func TestBuildServiceMap_TruncatesLowestVolumeEdges(t *testing.T) {
	owned := map[string]state.App{
		"a": {ID: "a", Slug: "a"},
		"b": {ID: "b", Slug: "b"},
		"c": {ID: "c", Slug: "c"},
	}
	samples := serviceMapSamples{calls: map[serviceMapEdgeKey]float64{
		{caller: "a", target: "b"}: 5,
		{caller: "b", target: "c"}: 50,
		{caller: "a", target: "c"}: 20,
	}}
	nodes, edges, truncated := buildServiceMap(samples, owned, 2)
	if !truncated || len(edges) != 2 {
		t.Fatalf("truncated=%v edges=%+v, want 2 edges and truncated", truncated, edges)
	}
	if edges[0].Calls != 50 || edges[1].Calls != 20 {
		t.Fatalf("edges not ranked by volume: %+v", edges)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %+v, want the three apps on surviving edges", nodes)
	}
}
