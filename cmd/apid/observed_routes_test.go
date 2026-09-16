package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

type vectorFixture struct {
	Labels map[string]string
	Value  string
}

func prometheusVectorServer(t *testing.T, health, routes []vectorFixture) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	queries := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		rows := routes
		if strings.Contains(query, `up{job="gatewayd-internal"}`) {
			rows = health
		}
		result := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			result = append(result, map[string]any{
				"metric": row.Labels,
				"value":  []any{float64(1), row.Value},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": result},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func seedRouteCollectors(t *testing.T, store state.Store, count int) []state.ComputeNode {
	t.Helper()
	nodes := make([]state.ComputeNode, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("route-node-%d.faas", i+1)
		gatewayTarget := fmt.Sprintf("tcp://10.0.0.%d:8080", i+10)
		node, err := store.UpsertComputeNodeFromOperator(t.Context(), state.ComputeNode{
			Name:               name,
			TargetURL:          fmt.Sprintf("tcp://10.0.0.%d:50051", i+10),
			GatewayTargetURL:   &gatewayTarget,
			VPCPUs:             4,
			MemMB:              8192,
			MaxConcurrency:     16,
			AdmissionCeilingMB: 4096,
		})
		if err != nil {
			t.Fatalf("seed collector %d: %v", i, err)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func routeHealth(nodes []state.ComputeNode, values ...string) []vectorFixture {
	out := make([]vectorFixture, 0, len(nodes))
	for i, node := range nodes {
		value := "1"
		if i < len(values) {
			value = values[i]
		}
		out = append(out, vectorFixture{Labels: map[string]string{"node_id": node.ID}, Value: value})
	}
	return out
}

func TestAppRoutesFleetAggregateMoreThanTwoNodes(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "fleet-routes")
	nodes := seedRouteCollectors(t, e.store, 3)
	prom, queries := prometheusVectorServer(t, routeHealth(nodes), []vectorFixture{
		{Labels: map[string]string{"node_id": nodes[0].ID, "route": "GET /from-node-a"}, Value: "4"},
		{Labels: map[string]string{"node_id": nodes[1].ID, "route": "POST /from-node-b"}, Value: "2"},
		{Labels: map[string]string{"node_id": nodes[2].ID, "route": "DELETE /from-node-c"}, Value: "1"},
	})
	e.s.WithStatusCache(prom.URL, "")
	// A production fleet read must not silently regress to this one hard-coded
	// gateway even when that legacy fallback is configured.
	e.s.WithGatewaydControlURL("http://127.0.0.1:1")

	rec := e.do(t, http.MethodGet, "/v1/apps/fleet-routes/routes", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.AppRoutesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Source != api.AppRoutesSourceLive || got.CollectorsExpected != 3 || got.CollectorsHealthy != 3 {
		t.Fatalf("fleet state=%+v", got)
	}
	wantRoutes := []string{"DELETE /from-node-c", "GET /from-node-a", "POST /from-node-b"}
	if fmt.Sprint(got.Routes) != fmt.Sprint(wantRoutes) {
		t.Fatalf("routes=%v want=%v", got.Routes, wantRoutes)
	}
	joinedQueries := strings.Join(*queries, "\n")
	if !strings.Contains(joinedQueries, `app="`+app.ID+`"`) {
		t.Fatalf("route query did not use authorized app id: %s", joinedQueries)
	}
}

func TestAppRoutesFleetStates(t *testing.T) {
	tests := []struct {
		name        string
		health      []string
		routes      []vectorFixture
		wantSource  string
		wantHealthy int
		wantRoutes  int
	}{
		{
			name:       "healthy no traffic is live empty",
			health:     []string{"1", "1", "1"},
			wantSource: api.AppRoutesSourceLive, wantHealthy: 3,
		},
		{
			name:       "one collector down is partial",
			health:     []string{"1", "0", "1"},
			routes:     []vectorFixture{{Labels: map[string]string{"route": "GET /still-visible"}, Value: "3"}},
			wantSource: api.AppRoutesSourcePartial, wantHealthy: 2, wantRoutes: 1,
		},
		{
			name:       "all collectors down is unavailable and drops stale rows",
			health:     []string{"0", "0", "0"},
			routes:     []vectorFixture{{Labels: map[string]string{"route": "GET /stale"}, Value: "3"}},
			wantSource: api.AppRoutesSourceUnavailable, wantHealthy: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			seedApp(t, e, "fleet-state")
			nodes := seedRouteCollectors(t, e.store, 3)
			for i := range tt.routes {
				tt.routes[i].Labels["node_id"] = nodes[0].ID
			}
			prom, _ := prometheusVectorServer(t, routeHealth(nodes, tt.health...), tt.routes)
			e.s.WithStatusCache(prom.URL, "")
			rec := e.do(t, http.MethodGet, "/v1/apps/fleet-state/routes", nil, nil)
			var got api.AppRoutesResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v body=%s", err, rec.Body.String())
			}
			if got.Source != tt.wantSource || got.CollectorsExpected != 3 || got.CollectorsHealthy != tt.wantHealthy || len(got.Routes) != tt.wantRoutes {
				t.Fatalf("got=%+v", got)
			}
			if rec.Header().Get("X-Faas-Routes-State") != tt.wantSource {
				t.Fatalf("state header=%q want=%q", rec.Header().Get("X-Faas-Routes-State"), tt.wantSource)
			}
		})
	}
}

func TestAutoOpenAPIAndPreviewPreservePartialFleetState(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "fleet-openapi")
	nodes := seedRouteCollectors(t, e.store, 3)
	prom, _ := prometheusVectorServer(t, routeHealth(nodes, "1", "0", "1"), []vectorFixture{
		{Labels: map[string]string{"node_id": nodes[0].ID, "route": "GET /observed"}, Value: "2"},
	})
	// Construct a fresh apid against the already-populated store and the
	// existing Prometheus data. No in-process gateway route map or cache is
	// carried across this boundary.
	restarted := newServer(e.store, e.s.log, "gregale.dev", noopNotifier{}).
		WithOpsMetrics(t.Context(), e.ops).
		WithStatusCache(prom.URL, "")
	e.s = restarted
	e.h = restarted.handler()

	auto := e.do(t, http.MethodGet, "/v1/apps/fleet-openapi/openapi?source=auto", nil, nil)
	if auto.Code != http.StatusOK {
		t.Fatalf("auto status=%d body=%s", auto.Code, auto.Body.String())
	}
	if got := auto.Header().Get("X-OpenAPI-Doc-Source"); got != openapidiff.SourceDegradedRoutesPartial {
		t.Fatalf("auto source=%q", got)
	}
	var doc struct {
		Paths    map[string]any `json:"paths"`
		Observed struct {
			Source             string `json:"source"`
			Available          bool   `json:"available"`
			CollectorsExpected int    `json:"collectors_expected"`
			CollectorsHealthy  int    `json:"collectors_healthy"`
		} `json:"x-faas-observed-routes"`
	}
	if err := json.Unmarshal(auto.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode auto: %v", err)
	}
	if _, ok := doc.Paths["/observed"]; !ok {
		t.Fatalf("observed route absent: %v", doc.Paths)
	}
	if doc.Observed.Source != api.AppRoutesSourcePartial || !doc.Observed.Available || doc.Observed.CollectorsExpected != 3 || doc.Observed.CollectorsHealthy != 2 {
		t.Fatalf("observed provenance=%+v", doc.Observed)
	}

	preview := e.do(t, http.MethodGet, "/v1/apps/fleet-openapi/openapi/preview", nil, nil)
	var got api.AppOpenAPIPolicyPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode preview: %v body=%s", err, preview.Body.String())
	}
	if got.AppID != app.ID || got.Source != openapidiff.SourceDegradedRoutesPartial || got.ObservedSource != api.AppRoutesSourcePartial || !got.ObservedAvailable || got.CollectorsExpected != 3 || got.CollectorsHealthy != 2 {
		t.Fatalf("preview=%+v", got)
	}
}
