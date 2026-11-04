package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
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
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "api")
	res := e.Do(t, "GET", "/v1/apps/api/route-priorities", nil, nil)
	if got := routePrioritiesFrom(t, res.Code, res.Body.String()); got.Source != api.RoutePrioritySourceNone || len(got.Routes) != 0 {
		t.Fatalf("default = %+v", got)
	}
	rules := []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: "critical"}, {Path: "/exports/*", Class: "bulk"}}
	res = e.Do(t, "PUT", "/v1/apps/api/route-priorities", api.SetRoutePrioritiesRequest{Routes: rules}, nil)
	got := routePrioritiesFrom(t, res.Code, res.Body.String())
	if got.Source != api.RoutePrioritySourceConfigured || len(got.Routes) != 2 || got.Routes[0] != rules[0] || got.UpdatedAt == "" {
		t.Fatalf("after put = %+v", got)
	}
	res = e.Do(t, "DELETE", "/v1/apps/api/route-priorities", nil, nil)
	if got := routePrioritiesFrom(t, res.Code, res.Body.String()); got.Source != api.RoutePrioritySourceNone || got.UpdatedAt != "" {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestRoutePrioritiesRejects(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "api")
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
		if res := e.Do(t, "PUT", "/v1/apps/api/route-priorities", body, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, res.Code, res.Body.String())
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		body := any(nil)
		if method == "PUT" {
			body = api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{}}
		}
		if res := e.Do(t, method, "/v1/apps/missing/route-priorities", body, nil); res.Code != http.StatusNotFound {
			t.Fatalf("%s missing app: %d", method, res.Code)
		}
	}
}

// adr: 20261026143210 — the catalog lists route controls for Hobby and above.
func TestRouteControlsRefuseFreePlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, e, "api")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/apps/api/route-priorities", nil},
		{"PUT", "/v1/apps/api/route-priorities", api.SetRoutePrioritiesRequest{Routes: []api.RoutePriorityRule{}}},
		{"DELETE", "/v1/apps/api/route-priorities", nil},
		{"GET", "/v1/apps/api/throttle-suggestions", nil},
		{"GET", "/v1/apps/api/route-lifecycle/history", nil},
	} {
		res := e.Do(t, c.method, c.path, c.body, nil)
		if res.Code != http.StatusPaymentRequired {
			t.Fatalf("%s %s on Free: %d %s, want 402", c.method, c.path, res.Code, res.Body.String())
		}
	}
}
