package apidserver_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/sales"
	"github.com/onebox-faas/faas/pkg/referrals"
)

type checkoutRouteStore struct {
	*referrals.PostgresStore
	account, code string
	calls         int
	err           error
}

func (s *checkoutRouteStore) CheckoutState(_ context.Context, account string) (referrals.CheckoutState, error) {
	s.account = account
	return referrals.CheckoutState{Eligible: true}, nil
}
func (s *checkoutRouteStore) AttributeCheckout(_ context.Context, account string, input referrals.Attribution, _ []string) (bool, error) {
	s.calls++
	s.account = account
	s.code = input.Code
	return s.err == nil, s.err
}

func TestSalesCheckoutRoutesBindSessionAndGateBeforeBilling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     bool
		failure error
		want    int
		calls   int
	}{
		{"browser checkout", false, nil, 402, 1},
		{"invalid code", false, referrals.ErrInvalidCheckoutCode, 400, 1},
		{"previous purchase", false, referrals.ErrCheckoutIneligible, 409, 1},
		{"existing referral", false, referrals.ErrAlreadyAttributed, 409, 1},
		{"store failure", false, errors.New("offline"), 503, 1},
		{"API key", true, nil, 403, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "operator@example.com", "buyer@example.com")
			st := *e.Store()
			if err := st.MarkAccountEmailVerified(t.Context(), e.Acct().ID); err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateAccountPlan(t.Context(), e.Acct().ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			store := &checkoutRouteStore{err: tc.failure}
			(*e.S()).WithSales(sales.Config{Enabled: true}, store)
			body := map[string]string{"plan": "pro", "salesperson_code": " poju43 "}
			var status int
			var text string
			if tc.key {
				r := e.Do(t, "PATCH", "/v1/account/plan", body, nil)
				status, text = r.Code, r.Body.String()
			} else {
				r := e.DoSession(t, "", "PATCH", "/v1/account/plan", body, nil)
				status, text = r.Code, r.Body.String()
			}
			if status != tc.want || store.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", status, store.calls, text)
			}
			if store.calls > 0 && (store.account != e.Acct().ID || store.code != "POJU43") {
				t.Fatalf("wrong attribution: %+v", store)
			}
			acct, err := st.AccountByID(t.Context(), e.Acct().ID)
			if err != nil || acct.Plan != api.PlanFree {
				t.Fatal("checkout changed entitlements", acct, err)
			}
			r := e.DoSession(t, "", "GET", "/v1/referrals/checkout", nil, nil)
			if r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Body.String(), `"eligible":true`) {
				t.Fatal(r.Code, r.Body.String())
			}
			if store.account != e.Acct().ID {
				t.Fatal("status queried another account")
			}
		})
	}
}
