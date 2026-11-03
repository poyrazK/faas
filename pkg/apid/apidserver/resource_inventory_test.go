// adr: 20261103222927
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestNativeResourceInventoryAppReadOnlyBoundary(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "inventory-reader")
	apidserver.MustSeedApp(t, e, "inventory-neighbor")
	if _, err := (*e.Store()).CreateCustomDomain(t.Context(), "api.inventory.test", id, "never-expose-challenge"); err != nil {
		t.Fatal(err)
	}
	original := *e.Key()
	mint := e.Do(t, http.MethodPost, "/v1/apps/inventory-reader/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeAppsRead}}, nil)
	var token api.DeployTokenResponse
	if mint.Code != 201 || json.Unmarshal(mint.Body.Bytes(), &token) != nil {
		t.Fatalf("mint: %d %s", mint.Code, mint.Body)
	}
	*e.Key() = token.Plaintext
	r := e.Do(t, http.MethodGet, "/v1/apps/inventory-reader/resource-inventory?kind=domains&limit=1", nil, nil)
	var out api.AppResourceInventoryResponse
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.AppID != id || len(out.Items) != 1 || out.Items[0].Domain != "api.inventory.test" || strings.Contains(r.Body.String(), "never-expose") {
		t.Fatalf("own metadata: %d %s", r.Code, r.Body)
	}
	for _, path := range []string{"/v1/domains", "/v1/postgres/databases", "/v1/apps"} {
		if r := e.Do(t, http.MethodGet, path, nil, nil); r.Code != 403 {
			t.Fatalf("app read gained account inventory %s: %d", path, r.Code)
		}
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/inventory-neighbor/resource-inventory?kind=domains", nil, nil); r.Code != 404 {
		t.Fatal("cross-app inventory became readable")
	}
	if r := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: "forbidden.inventory.test", AppID: id}, nil); r.Code != 403 {
		t.Fatalf("metadata read gained domain writes: %d", r.Code)
	}
	*e.Key() = original
	if r := e.Do(t, http.MethodDelete, "/v1/apps/inventory-reader/deploy-tokens/"+token.ID, nil, nil); r.Code != 204 {
		t.Fatal("could not revoke reader")
	}
	*e.Key() = token.Plaintext
	if r := e.Do(t, http.MethodGet, "/v1/apps/inventory-reader/resource-inventory?kind=domains", nil, nil); r.Code != 401 {
		t.Fatalf("revoked inventory credential remains usable: %d", r.Code)
	}
}
