package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type routePriorityCall struct {
	method string
	body   api.SetRoutePrioritiesRequest
}

func runRoutePriorityCLI(t *testing.T, args ...string) (int, string, []routePriorityCall) {
	t.Helper()
	var calls []routePriorityCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/route-priorities" {
			http.NotFound(w, r)
			return
		}
		call := routePriorityCall{method: r.Method}
		resp := api.RoutePrioritiesResponse{Slug: "demo", Source: api.RoutePrioritySourceRouteHealth,
			Routes: []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: "critical"}}}
		if r.Method == http.MethodPut {
			if err := json.NewDecoder(r.Body).Decode(&call.body); err != nil {
				t.Errorf("decode: %v", err)
			}
			resp.Source, resp.Routes = api.RoutePrioritySourceConfigured, call.body.Routes
		}
		calls = append(calls, call)
		writeJSONTest(w, resp)
	}))
	t.Cleanup(server.Close)
	resetJSONOut(t)
	setPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	code := run(append([]string{"routes", "priority"}, args...))
	return code, output.String(), calls
}

func TestRoutesPriorityCLIShowsDefault(t *testing.T) {
	code, out, calls := runRoutePriorityCLI(t, "demo")
	if code != 0 || len(calls) != 1 || calls[0].method != "GET" {
		t.Fatalf("exit %d calls %+v: %s", code, calls, out)
	}
	for _, want := range []string{"default: route-health routes are critical", "critical  POST    /checkout", "Crawlers and link-preview bots"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRoutesPriorityCLISetsRulesInOrder(t *testing.T) {
	code, out, calls := runRoutePriorityCLI(t, "demo", "--critical", "post /checkout", "--bulk", "/exports/*", "--critical", "GET /users/{id}")
	if code != 0 || len(calls) != 1 || calls[0].method != "PUT" {
		t.Fatalf("exit %d calls %+v: %s", code, calls, out)
	}
	want := []api.RoutePriorityRule{
		{Method: "POST", Path: "/checkout", Class: "critical"},
		{Method: "GET", Path: "/users/{id}", Class: "critical"},
		{Path: "/exports/*", Class: "bulk"},
	}
	got := calls[0].body.Routes
	if len(got) != len(want) {
		t.Fatalf("rules = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRoutesPriorityCLIClearAndReset(t *testing.T) {
	if code, _, calls := runRoutePriorityCLI(t, "demo", "--clear"); code != 0 || len(calls) != 1 || calls[0].method != "PUT" || calls[0].body.Routes == nil || len(calls[0].body.Routes) != 0 {
		t.Fatalf("clear: exit %d calls %+v", code, calls)
	}
	if code, _, calls := runRoutePriorityCLI(t, "demo", "--reset"); code != 0 || len(calls) != 1 || calls[0].method != "DELETE" {
		t.Fatalf("reset: exit %d calls %+v", code, calls)
	}
}

func TestRoutesPriorityCLIRejects(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"demo", "--critical", "POST /checkout extra"},
		{"demo", "--critical", "/checkout", "--reset"},
		{"demo", "--clear", "--bulk", "/x"},
		{"demo", "--bulk", "nope"},
	} {
		if code, _, calls := runRoutePriorityCLI(t, args...); code == 0 || len(calls) != 0 {
			t.Fatalf("args %v: exit %d calls %+v", args, code, calls)
		}
	}
}
