package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestInternalCIDisabledRoutesAreIndistinguishableFromMissingRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithInternalCIStorage(nil, "https://ci.example")
	paths := []string{
		"", "/", "/capabilities", "/validate", "/health", "/pipelines", "/pipelines/checks",
		"/revisions/revision", "/runs", "/runs/run", "/runs/run/cancel", "/runs/run/events",
		"/github/bindings", "/github/bindings/source", "/github/automation", "/github/automation/source",
		"/github/automation/source/events", "/github/automation/source/events/event",
		"/github/automation/source/events/event/retry", "/github/automation/source/checks",
		"/github/automation/source/events/event/check", "/github/automation/source/events/event/check/retry",
		"/executions", "/executions/run", "/executions/run/reports", "/executions/run/cancel",
		"/executions/run/rerun", "/executions/run/archive", "/executions/run/attempts/attempt/logs",
		"/executions/run/jobs/test/artifacts/report", "/profiles", "/profiles/profile",
		"/checks", "/checks/check", "/checks/check/retry", "/archives", "/archives/archive",
		"/runtime/run/control", "/runtime/run/source", "/runtime/run/objects", "/runtime/run/result",
		"/runtime/run/progress", "/runtime/run/inputs/report", "//runs",
	}
	handler := (*e.S()).Handler()
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			for _, key := range []string{"", "invalid-key", *e.Key()} {
				request := httptest.NewRequest(method, "https://ci.example/v1/internal/ci"+path, strings.NewReader("invalid body"))
				if key != "" {
					request.Header.Set("Authorization", "Bearer "+key)
				}
				request.Header.Set("Origin", "https://customer.example")
				request.Header.Set("Access-Control-Request-Method", http.MethodGet)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				assertCIHiddenRoute(t, recorder, method+" "+path)
			}
		}
	}
}

func assertCIHiddenRoute(t *testing.T, recorder *httptest.ResponseRecorder, route string) {
	t.Helper()
	var problem struct{ Code, Detail string }
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("%s: invalid problem: %v", route, err)
	}
	if recorder.Code != http.StatusNotFound || problem.Code != "not_found" || problem.Detail != "no such API route" {
		t.Fatalf("%s revealed CI: %d %s", route, recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s: disabled route response must not be cached", route)
	}
	for _, header := range []string{"Allow", "WWW-Authenticate", "Location", "Access-Control-Allow-Methods"} {
		if value := recorder.Header().Get(header); value != "" {
			t.Fatalf("%s revealed a route through %s: %s", route, header, value)
		}
	}
}

func TestInternalCIRemainsAbsentFromCustomerDiscovery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	for _, enabled := range []bool{false, true} {
		(*e.S()).WithInternalCIConfig(internalCIConfig(e.Acct().ID, enabled))
		for _, path := range []string{"/v1/capabilities", "/v1/openapi.yaml", "/v1/openapi.json"} {
			recorder := e.Do(t, http.MethodGet, path, nil, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("customer discovery unavailable: %s: %d", path, recorder.Code)
			}
			for _, hidden := range []string{"Gregale CI", "/v1/internal/ci", ci.InternalEnv, `"key":"ci"`} {
				if strings.Contains(recorder.Body.String(), hidden) {
					t.Fatalf("%s advertises internal CI with enabled=%v: %q", path, enabled, hidden)
				}
			}
		}
	}
}
