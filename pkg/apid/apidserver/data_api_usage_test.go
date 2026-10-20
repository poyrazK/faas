package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDataAPIUsageRoutesRequireTheirOwnCredentials(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "usage-api", 9002)
	serve := func(method, path, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"version":1,"report_id":"r","hours":[]}`))
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
	}
	// The runtime surface accepts only a usage workload token.
	for _, authorization := range []string{"", "Bearer " + *env.Key()} {
		if w := serve(http.MethodPost, "/v1/runtime/data-api/usage", authorization); w.Code != http.StatusUnauthorized {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// The owner view needs an account credential and the app in that account.
	if w := serve(http.MethodGet, "/v1/apps/usage-api/data-api/usage", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated read", w.Code)
	}
	if w := serve(http.MethodGet, "/v1/apps/not-mine/data-api/usage", "Bearer "+*env.Key()); w.Code != http.StatusNotFound {
		t.Fatal("unknown app", w.Code, w.Body.String())
	}
	// The in-memory test store has no usage tables.
	w := serve(http.MethodGet, "/v1/apps/usage-api/data-api/usage", "Bearer "+*env.Key())
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "data_api_usage_unavailable") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}
