package apidserver_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestGetOverageCap_SavedValue(t *testing.T) {
	for _, value := range []string{"null", "0", "1250"} {
		t.Run(value, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			var cents *int64
			if value != "null" {
				n := int64(0)
				if value == "1250" {
					n = 1250
				}
				cents = &n
			}
			if err := (*e.Store()).UpdateAccountOverageCapCents(context.Background(), (*e.Acct()).ID, cents); err != nil {
				t.Fatal(err)
			}
			other, err := (*e.Store()).CreateAccount(context.Background(), "other@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			otherCap := int64(9999)
			if err := (*e.Store()).UpdateAccountOverageCapCents(context.Background(), other.ID, &otherCap); err != nil {
				t.Fatal(err)
			}
			rec := e.Do(t, "GET", "/v1/account/overage-cap", nil, nil)
			if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"overage_cap_cents":`+value+`}` {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestGetOverageCap_RequiresAuthentication(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/account/overage-cap", nil))
	if rec.Code != 401 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestGetOverageCap_ReadScope(t *testing.T) {
	e := apidserver.SetupWithScopes(t, []string{api.ScopeAppsRead})
	rec := e.Do(t, "GET", "/v1/account/overage-cap", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

type failedOverageCapStore struct{ Store state.Store }

func (s failedOverageCapStore) GetAccountOverageCapCents(context.Context, string) (int64, bool, error) {
	return 0, false, errors.New("read unavailable")
}

func TestGetOverageCap_ReadFailure(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*(*e.S()).Store()) = statetest.NewStore(failedOverageCapStore{(*e.Store())})
	rec := e.Do(t, "GET", "/v1/account/overage-cap", nil, nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), `"overage_cap_cents":null`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// Setting or clearing the spend cap is a billing change, like the plan and
// billing routes: a deploy-scoped key (a CI key, or an MCP connection with
// writes) must not remove the account's spending ceiling. A refused call
// leaves the saved cap as it was.
func TestRaiseOverageCap_RequiresAdminScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		want   int
	}{
		{"deploy key", []string{api.ScopeDeployWrite}, 403},
		{"read and deploy key", []string{api.ScopeAppsRead, api.ScopeDeployWrite}, 403},
		{"admin key", api.ScopesAdminOnly, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.SetupWithScopes(t, tc.scopes)
			saved := int64(500)
			if err := (*e.Store()).UpdateAccountOverageCapCents(context.Background(), (*e.Acct()).ID, &saved); err != nil {
				t.Fatal(err)
			}
			rec := e.Do(t, "POST", "/v1/account/overage-cap", map[string]any{"overage_cap_cents": nil}, nil)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			cents, capped, err := (*e.Store()).GetAccountOverageCapCents(context.Background(), (*e.Acct()).ID)
			if err != nil {
				t.Fatal(err)
			}
			if cleared := !capped; cleared != (tc.want == 200) || capped && cents != saved {
				t.Fatalf("cap after the call = %d (capped %v)", cents, capped)
			}
		})
	}
}
