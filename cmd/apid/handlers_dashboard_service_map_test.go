package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderServiceMapPage(t *testing.T, e testEnv, path string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r = r.WithContext(WithAccount(r.Context(), e.acct))
	rec := httptest.NewRecorder()
	e.s.renderServiceMapDashboard(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

func TestServiceMapDashboard_States(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		enabled bool
		want    string
	}{
		{name: "disabled", plan: api.PlanHobby, enabled: false, want: "not enabled for this deployment"},
		{name: "free plan", plan: api.PlanFree, enabled: true, want: "start on the Hobby plan"},
		{name: "no prometheus", plan: api.PlanHobby, enabled: true, want: "Metrics are unavailable right now (prometheus not configured)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, tc.plan)
			e.s.serviceMapEnabled = tc.enabled
			if body := renderServiceMapPage(t, e, "/dashboard/service-map"); !strings.Contains(body, tc.want) {
				t.Fatalf("page missing %q:\n%s", tc.want, body)
			}
		})
	}
}

func TestServiceMapDashboard_RendersEdges(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	caller := createApp(t, e, "public-api")
	target := createApp(t, e, "payments")
	var gotRange string
	installPromFixture(t, &e, func(query string) string {
		if !strings.Contains(query, "gateway_service_dependency_edge_calls_total") {
			return `{"data":{"resultType":"vector","result":[]}}`
		}
		gotRange = query[strings.LastIndex(query, "[")+1 : strings.LastIndex(query, "]")]
		return fmt.Sprintf(`{"data":{"resultType":"vector","result":[`+
			`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"success"},"value":[1,"90"]},`+
			`{"metric":{"caller_app":"%[1]s","target_app":"%[2]s","outcome":"error"},"value":[1,"10"]}]}}`,
			canonicalServiceMapAppID(caller.ID), canonicalServiceMapAppID(target.ID))
	})

	body := renderServiceMapPage(t, e, "/dashboard/service-map?range=24h")
	if gotRange != "24h" {
		t.Fatalf("query window = %q, want 24h", gotRange)
	}
	for _, want := range []string{
		`<a href="/dashboard/apps/public-api">public-api</a>`,
		`<a href="/dashboard/apps/payments">payments</a>`,
		`<td class="num">100</td>`,
		`<td class="num bad">10 (10.00%)</td>`,
		`<strong>24h</strong>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestServiceMapDashboard_InvalidRangeFallsBackToDefault(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	createApp(t, e, "quiet")
	installPromFixture(t, &e, func(string) string { return `{"data":{"resultType":"vector","result":[]}}` })

	body := renderServiceMapPage(t, e, "/dashboard/service-map?range=99y")
	if !strings.Contains(body, "<strong>"+api.ServiceMapDefaultRange+"</strong>") {
		t.Fatalf("invalid range should select the default window:\n%s", body)
	}
	if !strings.Contains(body, "No calls between your apps in the last "+api.ServiceMapDefaultRange) {
		t.Fatalf("empty map should say so:\n%s", body)
	}
}

// adr: 732 — the dashboard route sits behind the session chain like its peers.
func TestServiceMapDashboard_RequiresSession(t *testing.T) {
	e := serviceMapEnv(t, api.PlanHobby)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/service-map", nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), loginPath) {
		t.Fatalf("unauthenticated GET = %d %q, want redirect to %s", rec.Code, rec.Header().Get("Location"), loginPath)
	}
}
