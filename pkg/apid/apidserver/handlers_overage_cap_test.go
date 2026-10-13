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
