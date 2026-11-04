// adr: 20261103131742 — amendment 2: egress past a paid plan's monthly
// allowance is billed only for a subject that turned egress overage on.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func egressOverage(t *testing.T, e apidserver.TestEnv, method string, body any) (int, api.EgressOverageResponse, string) {
	t.Helper()
	rec := e.Do(t, method, "/v1/account/egress-overage", body, nil)
	var out api.EgressOverageResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}
	return rec.Code, out, rec.Body.String()
}

func TestEgressOverageIsOffUntilTurnedOn(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithBillingProvider(apidserver.NewLiveEgressProvider())
	code, out, body := egressOverage(t, e, http.MethodGet, nil)
	want := api.EgressOverageResponse{Billed: true, IncludedGiB: 500, MillicentsPerGiB: 3_000}
	if code != http.StatusOK || out != want {
		t.Fatalf("GET = %d %+v (%s), want %+v", code, out, body, want)
	}
	code, out, body = egressOverage(t, e, http.MethodPut, map[string]any{"enabled": true})
	want.Enabled = true
	if code != http.StatusOK || out != want {
		t.Fatalf("PUT on = %d %+v (%s), want %+v", code, out, body, want)
	}
	store := *e.Store()
	if on, err := store.EgressOverageEnabled(t.Context(), e.Acct().ID); err != nil || !on {
		t.Fatalf("saved choice = %v, %v; want on", on, err)
	}
	if code, out, body = egressOverage(t, e, http.MethodPut, map[string]any{"enabled": false}); code != http.StatusOK || out.Enabled {
		t.Fatalf("PUT off = %d %+v (%s)", code, out, body)
	}
	choices, err := store.EgressOverageChoices(t.Context(), e.Acct().ID, time.Unix(0, 0), time.Now().Add(time.Minute))
	if err != nil || len(choices) != 2 {
		t.Fatalf("choices = %+v, %v; want the on and the off kept", choices, err)
	}
	if code, _, body := egressOverage(t, e, http.MethodPut, map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("PUT without enabled = %d %s, want 400", code, body)
	}
}

func TestEgressOverageRefusedOnFree(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	code, _, body := egressOverage(t, e, http.MethodPut, map[string]any{"enabled": true})
	var problem api.Problem
	if code != http.StatusForbidden || json.Unmarshal([]byte(body), &problem) != nil || problem.Code != api.CodePlanEgressOverageNotAllowed {
		t.Fatalf("Free PUT on = %d %s, want 403 %s", code, body, api.CodePlanEgressOverageNotAllowed)
	}
	if code, out, body := egressOverage(t, e, http.MethodPut, map[string]any{"enabled": false}); code != http.StatusOK || out.Enabled || out.Billed {
		t.Fatalf("Free PUT off = %d %+v (%s)", code, out, body)
	}
}

func TestEgressOverageNeedsAnAdminCredential(t *testing.T) {
	e := apidserver.SetupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	if code, _, body := egressOverage(t, e, http.MethodPut, map[string]any{"enabled": true}); code != http.StatusForbidden {
		t.Fatalf("deploy key PUT = %d %s, want 403", code, body)
	}
	if code, _, body := egressOverage(t, e, http.MethodGet, nil); code != http.StatusOK {
		t.Fatalf("read key GET = %d %s, want 200", code, body)
	}
}

// Egress past the allowance is not overage in the usage summary while egress
// overage is off: the apps stop there and those bytes are not billed.
func TestUsageSummary_EgressOverageOffBillsNoEgress(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithBillingProvider(apidserver.NewLiveEgressProvider())
	if err := (*e.Store()).AppendUsage(t.Context(), e.Acct().ID, "app-egress", "instance-egress", time.Now().UTC(),
		0, 0, 0, 0, 2*(1<<30), 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodGet, "/v1/usage/summary", nil, nil)
	var out api.UsageSummaryResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("GET /v1/usage/summary = %d %s", rec.Code, rec.Body)
	}
	if out.EgressOverageGB != 0 || out.OverageCents != 0 || out.UsedEgressGB != 2 {
		t.Fatalf("summary = egress %v GiB, overage %v GiB, %d cents; want 2 GiB used and nothing billed", out.UsedEgressGB, out.EgressOverageGB, out.OverageCents)
	}
}
