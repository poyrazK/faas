package apidserver_test

// adr: 20261104204500

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func decodeEgressOverage(t *testing.T, body []byte) api.EgressOverageResponse {
	t.Helper()
	var out api.EgressOverageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

// A paid account elects egress overage, turns it off and on again; the start
// is kept and the plan's allowance is reported. Without a provider that bills
// egress live, billing is off.
func TestEgressOverage_ElectTurnOffAndBack(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "GET", "/v1/account/egress-overage", nil, nil)
	got := decodeEgressOverage(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || got.Enabled || !got.Electable || got.IncludedGiB != 500 || got.Billing != api.EgressOverageBillingOff || got.EnabledAt != nil {
		t.Fatalf("initial = %d %+v", rec.Code, got)
	}

	rec = e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": true}, nil)
	on := decodeEgressOverage(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || !on.Enabled || on.EnabledAt == nil || on.DisabledAt != nil {
		t.Fatalf("turn on = %d %+v", rec.Code, on)
	}
	rec = e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": false}, nil)
	off := decodeEgressOverage(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || off.Enabled || off.DisabledAt == nil || !off.EnabledAt.Equal(*on.EnabledAt) {
		t.Fatalf("turn off = %d %+v", rec.Code, off)
	}
	rec = e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": true}, nil)
	again := decodeEgressOverage(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || !again.Enabled || again.DisabledAt != nil || !again.EnabledAt.Equal(*on.EnabledAt) {
		t.Fatalf("turn on again = %d %+v, want the first start kept", rec.Code, again)
	}
}

// Free cannot elect egress overage, but turning it off is always allowed.
func TestEgressOverage_FreeCannotElect(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	rec := e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": true}, nil)
	if rec.Code != http.StatusPaymentRequired || !jsonHasCode(rec.Body.Bytes(), api.CodePlanEgressOverageNotAllowed) {
		t.Fatalf("free election = %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": false}, nil)
	if got := decodeEgressOverage(t, rec.Body.Bytes()); rec.Code != http.StatusOK || got.Enabled || got.Electable || got.IncludedGiB != 10 {
		t.Fatalf("free turn off = %d %+v", rec.Code, got)
	}
}

// enabled is required; a deploy-scoped key cannot change the election.
func TestEgressOverage_RequestValidationAndScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if rec := e.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled = %d %s", rec.Code, rec.Body)
	}
	deploy := apidserver.SetupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	if rec := deploy.Do(t, "PUT", "/v1/account/egress-overage", map[string]any{"enabled": true}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("deploy key = %d %s", rec.Code, rec.Body)
	}
	if rec := deploy.Do(t, "GET", "/v1/account/egress-overage", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("deploy key read = %d %s", rec.Code, rec.Body)
	}
}

func jsonHasCode(body []byte, code string) bool {
	var p struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(body, &p) == nil && p.Code == code
}
