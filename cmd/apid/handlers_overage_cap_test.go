package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetOverageCap_SavedValue(t *testing.T) {
	for _, value := range []string{"null", "0", "1250"} {
		t.Run(value, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			var cents *int64
			if value != "null" {
				n := int64(0)
				if value == "1250" {
					n = 1250
				}
				cents = &n
			}
			if err := e.store.UpdateAccountOverageCapCents(context.Background(), e.acct.ID, cents); err != nil {
				t.Fatal(err)
			}
			other, err := e.store.CreateAccount(context.Background(), "other@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			otherCap := int64(9999)
			if err := e.store.UpdateAccountOverageCapCents(context.Background(), other.ID, &otherCap); err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, "GET", "/v1/account/overage-cap", nil, nil)
			if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"overage_cap_cents":`+value+`}` {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestGetOverageCap_RequiresAuthentication(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/account/overage-cap", nil))
	if rec.Code != 401 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestGetOverageCap_ReadScope(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	rec := e.do(t, "GET", "/v1/account/overage-cap", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

type failedOverageCapStore struct{ state.Store }

func (s failedOverageCapStore) GetAccountOverageCapCents(context.Context, string) (int64, bool, error) {
	return 0, false, errors.New("read unavailable")
}

func TestGetOverageCap_ReadFailure(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.store = failedOverageCapStore{e.store}
	rec := e.do(t, "GET", "/v1/account/overage-cap", nil, nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), `"overage_cap_cents":null`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}
