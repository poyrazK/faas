// adr: 521 — customer operations preserve ownership, execution fences and independent delivery.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHTTPRouteEntersOperationBeforeWake(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "operation-gateway@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "exports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID,
		Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer"}},"additionalProperties":false}`), OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}}}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	policy := operations.PreviewPolicy{Version: 1, Enabled: true, NotBefore: time.Now().UTC().Add(-time.Minute), ExpiresAt: time.Now().UTC().Add(time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: uuid.MustParse(account.ID).String(), AppID: uuid.MustParse(app.ID).String(), Scope: dep.Scope, PlatformTenantIDs: []string{tenant.ID}}}}
	policyPath := filepath.Join(t.TempDir(), "preview.json")
	writePolicy := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(policyPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	writePolicy(raw)
	gate, err := operations.NewPreviewAdmission(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Handler{}).WithOperationRoutes(DurableOperationRoutes{Store: store, Admission: gate})
	gatewayApp := App{ID: app.ID, AccountID: account.ID, Plan: account.Plan, RequestInvocationsEnabled: true}
	request := func(payload string, trusted bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "https://exports.gregale.dev/exports", strings.NewReader(payload))
		r.Header.Set("Idempotency-Key", "one-export")
		r.Header.Set(api.OperationIDHeader, "forged")
		if trusted {
			r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: tenant.ID}))
		}
		w := httptest.NewRecorder()
		if !h.applyOperationRoute(w, r, gatewayApp, "") {
			t.Fatal("matched route did not enter operation admission")
		}
		return w
	}
	if rec := request(`{"count":1}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("unowned ingress: %d %s", rec.Code, rec.Body.String())
	}
	rec := request(`{"count":1}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", rec.Code, rec.Body.String())
	}
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	op, err := store.OperationByID(ctx, account.ID, tenant.ID, receipt.ID)
	if err != nil || op.DefinitionID != def.ID || op.State != api.OperationAccepted {
		t.Fatalf("customer operation: %+v %v", op, err)
	}
	inv, err := store.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil || inv.State != state.InvocationPending || inv.InstanceID != "" {
		t.Fatalf("acceptance woke an instance: %+v %v", inv, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil || headers[api.OperationIDHeader] != "" {
		t.Fatal("public operation metadata entered trusted execution context")
	}
	duplicate := request(`{"count":1.0}`, true)
	if duplicate.Code != http.StatusAccepted || duplicate.Body.String() != rec.Body.String() {
		t.Fatalf("same-key ingress: %d %s", duplicate.Code, duplicate.Body.String())
	}
	if conflict := request(`{"count":2}`, true); conflict.Code != http.StatusConflict {
		t.Fatalf("changed input: %d %s", conflict.Code, conflict.Body.String())
	}
	owner := tenant
	tenant, _, err = store.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	if denied := request(`{"count":1}`, true); denied.Code != http.StatusServiceUnavailable {
		t.Fatalf("out-of-cohort customer: %d", denied.Code)
	}
	tenant = owner
	for _, replacement := range [][]byte{[]byte(`{"version":1,"enabled":false}`), []byte(`{"version":1,"enabled":`)} {
		writePolicy(replacement)
		if closed := request(`{"count":1}`, true); closed.Code != http.StatusServiceUnavailable {
			t.Fatalf("closed route escaped to ordinary handler: %d", closed.Code)
		}
	}
	if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	if closed := request(`{"count":1}`, true); closed.Code != http.StatusServiceUnavailable {
		t.Fatal("removed policy allowed ordinary handler execution")
	}
	h = (&Handler{}).WithOperationRoutes(DurableOperationRoutes{Store: store})
	if closed := request(`{"count":1}`, true); closed.Code != http.StatusServiceUnavailable {
		t.Fatal("default closed resolver did not fence retained definitions")
	}
	upgrade := httptest.NewRequest(http.MethodPost, "https://exports.gregale.dev/exports", strings.NewReader(`{"count":1}`))
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	upgraded := httptest.NewRecorder()
	if !h.applyOperationRoute(upgraded, upgrade, gatewayApp, "") || upgraded.Code != http.StatusBadRequest {
		t.Fatal("protocol upgrade bypassed the operation route fence")
	}
	retained, err := store.OperationByID(ctx, account.ID, owner.ID, receipt.ID)
	if err != nil || retained.CurrentInvocationID != inv.ID || retained.State != api.OperationAccepted {
		t.Fatal("cohort rollback altered admitted execution", err)
	}
	for _, header := range []string{api.OperationCapabilityHeader, api.OperationIDHeader, "X-Gregale-Operation-Execution-Kind", "X-Gregale-Customer-Operation-Unknown"} {
		if forged := asyncRouteHeaders(http.Header{header: []string{"forged"}, "X-Export-Format": []string{"csv"}}); len(forged) != 1 || forged["X-Export-Format"] != "csv" {
			t.Fatalf("reserved header %s survived public async envelope: %v", header, forged)
		}
	}
	ordinary := httptest.NewRequest("GET", "https://exports.gregale.dev/other", nil)
	if h.applyOperationRoute(httptest.NewRecorder(), ordinary, gatewayApp, "") {
		t.Fatal("unrelated handler was converted to an operation")
	}
}
