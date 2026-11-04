// adr: 20261103211147
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestNativeAppInspectionPreservesCredentialBoundary(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "inspection-app")
	apidserver.MustSeedApp(t, e, "inspection-neighbor")
	original := *e.Key()
	created := e.Do(t, http.MethodPost, "/v1/apps/inspection-app/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeUsageRead}}, nil)
	if created.Code != 201 {
		t.Fatalf("mint app billing reader: %d %s", created.Code, created.Body)
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	*e.Key() = token.Plaintext
	costs := e.Do(t, http.MethodGet, "/v1/apps/inspection-app/costs", nil, nil)
	if costs.Code != 200 || costs.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("app costs: %d %s", costs.Code, costs.Body)
	}
	var out api.AppCostsResponse
	if err := json.Unmarshal(costs.Body.Bytes(), &out); err != nil || out.AppID != appID || out.BillEstimateAvailable {
		t.Fatal("invalid app cost projection")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(costs.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"account_id", "invoices", "forecast", "price_contracts"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("app costs exposed %s", key)
		}
	}
	preview := e.Do(t, http.MethodGet, "/v1/apps/inspection-app/operation-preview?operation=env_change", nil, nil)
	var policy api.AppOperationPreview
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &policy) != nil || policy.Authorization != "blocked" || policy.Checks["scope"].Reason != "scope_required" {
		t.Fatalf("scope preview: %d %s", preview.Code, preview.Body)
	}
	for _, path := range []string{"/v1/apps/inspection-neighbor/costs", "/v1/apps/inspection-neighbor/operation-preview?operation=env_change"} {
		if response := e.Do(t, http.MethodGet, path, nil, nil); response.Code != 404 {
			t.Fatalf("cross-app inspection: %s %d", path, response.Code)
		}
	}
	if response := e.Do(t, http.MethodGet, "/v1/billing/costs", nil, nil); response.Code != 403 {
		t.Fatal("app billing reader gained account billing")
	}
	if response := e.Do(t, http.MethodPost, "/v1/apps/inspection-app/restart", nil, nil); response.Code != 403 {
		t.Fatal("billing inspection gained mutation scope")
	}
	for _, query := range []string{"?month=wrong", "?month=0001-01", "?month=9999-01", "?month=2026-10&month=2026-09", "?account_id=other", "?environment=staging"} {
		if response := e.Do(t, http.MethodGet, "/v1/apps/inspection-app/costs"+query, nil, nil); response.Code != 400 {
			t.Fatalf("invalid app cost query accepted: %s", query)
		}
	}
	rows, err := (*e.Store()).ListConfigRevisions(t.Context(), appID, "default", 20)
	if err != nil || len(rows) != 0 {
		t.Fatal("inspection created configuration history")
	}
	*e.Key() = original
	if revoked := e.Do(t, http.MethodDelete, "/v1/apps/inspection-app/deploy-tokens/"+token.ID, nil, nil); revoked.Code != 204 {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body)
	}
	*e.Key() = token.Plaintext
	if response := e.Do(t, http.MethodGet, "/v1/apps/inspection-app/costs", nil, nil); response.Code != 401 {
		t.Fatalf("revoked app billing reader remains usable: %d", response.Code)
	}
}

func TestNativeOperationPreviewDoesNotReserveAdmission(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "native-preview")
	for _, operation := range []string{"env_change", "restart", "scale"} {
		r := e.Do(t, http.MethodGet, "/v1/apps/native-preview/operation-preview?operation="+operation, nil, nil)
		var out api.AppOperationPreview
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.AppID != appID || out.Authorization != "allowed" || out.Admission == "allowed" {
			t.Fatalf("preview %s: %d %s", operation, r.Code, r.Body)
		}
	}
	for _, query := range []string{"", "?operation=delete", "?operation=scale&operation=restart", "?operation=scale&account_id=other"} {
		if r := e.Do(t, http.MethodGet, "/v1/apps/native-preview/operation-preview"+query, nil, nil); r.Code != 400 {
			t.Fatalf("invalid preview query accepted: %s", query)
		}
	}
}
