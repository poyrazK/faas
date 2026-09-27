package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppPreAuthObservationsFreePlanAndPolicyScope(t *testing.T) {
	e := setup(t, api.PlanFree)
	config := &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 2,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 2,
			FailedResponses: &api.PreAuthFailedResponseLimit{FailuresPerMinute: 5, Burst: 1}}},
	}
	created := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "protected-app", PreAuthRateLimit: config}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	var createdApp api.AppResponse
	if err := json.Unmarshal(created.Body.Bytes(), &createdApp); err != nil {
		t.Fatal(err)
	}
	key := "0"
	fixture := fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"policy":"app","outcome":"would_block"},"value":[0,"4"]},
		{"metric":{"policy":"route_%s","outcome":"result_2xx"},"value":[0,"2"]},
		{"metric":{"policy":"failures_%s","outcome":"result_4xx"},"value":[0,"1"]},
		{"metric":{"policy":"foreign","outcome":"would_block"},"value":[0,"999"]}
	]}}`, key, key)
	installPromFixture(t, &e, func(query string) string {
		if !strings.Contains(query, `gateway_pre_auth_policy_shadow_total`) || !strings.Contains(query, `[5m]`) ||
			!strings.Contains(query, fmt.Sprintf("app=%q", createdApp.ID)) {
			t.Errorf("unexpected PromQL query: %s", query)
		}
		return fixture
	})
	rec := e.do(t, "GET", "/v1/apps/protected-app/pre-auth-observations", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("observations = %d: %s", rec.Code, rec.Body.String())
	}
	var out api.PreAuthObservationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "prometheus" || out.Range != "5m" || out.AppID != createdApp.ID || len(out.Policies) != 3 {
		t.Fatalf("observations = %+v", out)
	}
	if out.Policies[0].PolicyID != "app" || out.Policies[0].WouldBlock != 4 ||
		out.Policies[1].PolicyID != "route_"+key || out.Policies[1].Result2xx != 2 ||
		out.Policies[2].PolicyID != "failures_"+key || out.Policies[2].Result4xx != 1 {
		t.Fatalf("policy mapping = %+v", out.Policies)
	}
	if rec := e.do(t, "GET", "/v1/apps/protected-app/pre-auth-observations?range=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid range = %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/apps/other-app/pre-auth-observations", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app = %d", rec.Code)
	}
}

func TestAppPreAuthObservationsDegraded(t *testing.T) {
	e := setup(t, api.PlanFree)
	config := &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 1}
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "protected-app", PreAuthRateLimit: config}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, "GET", "/v1/apps/protected-app/pre-auth-observations", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded observations = %d: %s", rec.Code, rec.Body.String())
	}
	var out api.PreAuthObservationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Source, "degraded:") || len(out.Policies) != 1 || out.Policies[0].WouldBlock != 0 {
		t.Fatalf("degraded observations = %+v", out)
	}
}
