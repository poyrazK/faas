package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func decodeServiceWakeAhead(t *testing.T, body string) api.ServiceWakeAheadResponse {
	t.Helper()
	var out api.ServiceWakeAheadResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestServiceWakeAheadSetting(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "api")
	res := e.do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get: %d %s", res.Code, res.Body.String())
	}
	if got := decodeServiceWakeAhead(t, res.Body.String()); got.Slug != "api" || got.Enabled || got.UpdatedAt != "" {
		t.Fatalf("default = %+v, want off and never set", got)
	}
	res = e.do(t, "PUT", "/v1/apps/api/service-wake-ahead", map[string]any{"enabled": true}, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("put: %d %s", res.Code, res.Body.String())
	}
	if got := decodeServiceWakeAhead(t, res.Body.String()); !got.Enabled || got.UpdatedAt == "" {
		t.Fatalf("after put = %+v", got)
	}
	res = e.do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	if got := decodeServiceWakeAhead(t, res.Body.String()); !got.Enabled {
		t.Fatalf("read back = %+v", got)
	}
	enabled, err := e.store.ServiceWakeAheadEnabled(t.Context(), appID)
	if err != nil || !enabled {
		t.Fatalf("gateway read = %v, %v", enabled, err)
	}
}

func TestServiceWakeAheadRejects(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "api")
	for _, body := range []any{map[string]any{}, map[string]any{"enabled": "yes"}, strings.Repeat("x", 2048)} {
		if res := e.do(t, "PUT", "/v1/apps/api/service-wake-ahead", body, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("body %v: %d %s", body, res.Code, res.Body.String())
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		if res := e.do(t, method, "/v1/apps/missing/service-wake-ahead", map[string]any{"enabled": true}, nil); res.Code != http.StatusNotFound {
			t.Fatalf("%s missing app: %d", method, res.Code)
		}
	}
}
