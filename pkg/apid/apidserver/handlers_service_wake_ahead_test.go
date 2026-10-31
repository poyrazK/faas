package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
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
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "api")
	res := e.Do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get: %d %s", res.Code, res.Body.String())
	}
	if got := decodeServiceWakeAhead(t, res.Body.String()); got.Slug != "api" || got.Enabled || got.UpdatedAt != "" {
		t.Fatalf("default = %+v, want off and never set", got)
	}
	res = e.Do(t, "PUT", "/v1/apps/api/service-wake-ahead", map[string]any{"enabled": true}, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("put: %d %s", res.Code, res.Body.String())
	}
	if got := decodeServiceWakeAhead(t, res.Body.String()); !got.Enabled || got.UpdatedAt == "" {
		t.Fatalf("after put = %+v", got)
	}
	res = e.Do(t, "GET", "/v1/apps/api/service-wake-ahead", nil, nil)
	if got := decodeServiceWakeAhead(t, res.Body.String()); !got.Enabled {
		t.Fatalf("read back = %+v", got)
	}
	enabled, err := (*e.Store()).ServiceWakeAheadEnabled(t.Context(), appID)
	if err != nil || !enabled {
		t.Fatalf("gateway read = %v, %v", enabled, err)
	}
}

// ADR-20261025105812: a project service under the project policy that never
// chose reports wake-ahead on, with no updated_at; an explicit off sticks.
func TestServiceWakeAheadProjectDefault(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	seeded := apidserver.MustSeedApp(t, e, "seed")
	store := *e.Store()
	owner, err := store.AppByID(t.Context(), seeded)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: owner.AccountID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: owner.AccountID, ProjectID: project.ID, Slug: "web",
		WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1,
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}}); err != nil {
		t.Fatal(err)
	}
	res := e.Do(t, "GET", "/v1/apps/web/service-wake-ahead", nil, nil)
	if got := decodeServiceWakeAhead(t, res.Body.String()); res.Code != http.StatusOK || !got.Enabled || got.UpdatedAt != "" {
		t.Fatalf("project default = %d %+v, want on and never set", res.Code, got)
	}
	if res := e.Do(t, "PUT", "/v1/apps/web/service-wake-ahead", map[string]any{"enabled": false}, nil); res.Code != http.StatusOK {
		t.Fatalf("put: %d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, "GET", "/v1/apps/web/service-wake-ahead", nil, nil)
	if got := decodeServiceWakeAhead(t, res.Body.String()); got.Enabled || got.UpdatedAt == "" {
		t.Fatalf("after explicit off = %+v", got)
	}
}

func TestServiceWakeAheadRejects(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "api")
	for _, body := range []any{map[string]any{}, map[string]any{"enabled": "yes"}, strings.Repeat("x", 2048)} {
		if res := e.Do(t, "PUT", "/v1/apps/api/service-wake-ahead", body, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("body %v: %d %s", body, res.Code, res.Body.String())
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		if res := e.Do(t, method, "/v1/apps/missing/service-wake-ahead", map[string]any{"enabled": true}, nil); res.Code != http.StatusNotFound {
			t.Fatalf("%s missing app: %d", method, res.Code)
		}
	}
}
