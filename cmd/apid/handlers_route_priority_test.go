package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func routePrioritiesFrom(t *testing.T, code int, body string) api.RoutePrioritiesResponse {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("%d: %s", code, body)
	}
	var out api.RoutePrioritiesResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRoutePrioritiesLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "api")
	res := e.do(t, "GET", "/v1/apps/api/route-priorities", nil, nil)
	if got := routePrioritiesFrom(t, res.Code, res.Body.String()); got.Source != api.RoutePrioritySourceNone || len(got.Routes) != 0 {
		t.Fatalf("default = %+v", got)
	}
	rules := []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: "critical"}, {Path: "/exports/*", Class: "bulk"}}
	res = e.do(t, "PUT", "/v1/apps/api/route-priorities", api.SetRoutePrioritiesRequest{Routes: rules}, nil)
	got := routePrioritiesFrom(t, res.Code, res.Body.String())
	if got.Source != api.RoutePrioritySourceConfigured || len(got.Routes) != 2 || got.Routes[0] != rules[0] || got.UpdatedAt == "" {
		t.Fatalf("after put = %+v", got)
	}
	res = e.do(t, "DELETE", "/v1/apps/api/route-priorities", nil, nil)
	if got := routePrioritiesFrom(t, res.Code, res.Body.String()); got.Source != api.RoutePrioritySourceNone || got.UpdatedAt != "" {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestRoutePrioritiesRejects(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "api")
	tooMany := make([]api.RoutePriorityRule, api.RoutePriorityMaxRules+1)
	for i := range tooMany {
		tooMany[i] = api.RoutePriorityRule{Path: "/a", Class: "bulk"}
	}
	for name, body := range map[string]any{
		"missing routes": map[string]any{},
		"bad class":      api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{{Path: "/a", Class: "urgent"}}},
		"bad method":     api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{{Method: "get", Path: "/a", Class: "bulk"}}},
		"bad path":       api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{{Path: "a", Class: "bulk"}}},
		"too many":       api.SetRoutePrioritiesRequest{Routes: tooMany},
	} {
		if res := e.do(t, "PUT", "/v1/apps/api/route-priorities", body, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, res.Code, res.Body.String())
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		body := any(nil)
		if method == "PUT" {
			body = api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{}}
		}
		if res := e.do(t, method, "/v1/apps/missing/route-priorities", body, nil); res.Code != http.StatusNotFound {
			t.Fatalf("%s missing app: %d", method, res.Code)
		}
	}
}
