package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Billing connections belong to an account, and a company app keeps its
// creator's, so its sign-up saves customers' cards in the creator's Stripe
// account and its customer portal opens there. Once the creator leaves the
// company, the company's customers must stop reaching that account: sign-up
// closes, a pending sign-up cannot complete and the portal is unavailable.
// A customer still reads their own usage.
//
// adr: 20261026151103
func TestCompanyAppSignupAndPortalStopWhenTheCreatorLeaves(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanPro)
	stripe := &consumerSignupStripe{complete: true}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	(*e.S()).ConsumerBilling().Stripe = client
	store := *e.Store()
	creator := *e.Acct()
	ok := func(t *testing.T, res *httptest.ResponseRecorder, want int, out any) {
		t.Helper()
		if res.Code != want {
			t.Fatalf("status %d, want %d: %s", res.Code, want, res.Body)
		}
		if out != nil {
			if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
				t.Fatalf("decode: %v: %s", err, res.Body)
			}
		}
	}
	org := apidserver.SeedSharedOrgWithOwner(t, e, "forecast-co", "Forecast Co", api.PlanPro)
	ok(t, e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "forecast"}, nil), http.StatusCreated, nil)
	ok(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)
	ok(t, e.Do(t, http.MethodPut, "/v1/apps/forecast/consumer-signup", api.PutConsumerSignupRequest{DisplayName: "Forecast API"}, nil), http.StatusOK, nil)

	public := map[string]string{"Authorization": ""}
	signup := func() api.CreateConsumerSignupResponse {
		t.Helper()
		var started api.CreateConsumerSignupResponse
		ok(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/forecast", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada"}, public),
			http.StatusCreated, &started)
		return started
	}
	complete := func(s api.CreateConsumerSignupResponse) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/consumer-signup/forecast/signups/"+s.ID+"/complete", api.CompleteConsumerSignupRequest{ClaimToken: s.ClaimToken}, public)
	}
	var done api.CompleteConsumerSignupResponse
	ok(t, complete(signup()), http.StatusOK, &done)
	customer := map[string]string{"Authorization": "Bearer " + done.Key}
	ok(t, e.Do(t, http.MethodPost, "/v1/consumer-billing/forecast/portal", nil, customer), http.StatusOK, nil)
	pending := signup()

	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if res := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); res.Code >= 300 {
		t.Fatalf("remove the creator: %d %s", res.Code, res.Body)
	}
	customers := len(stripe.customers)

	if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/forecast", nil, public); res.Code != http.StatusNotFound {
		t.Fatalf("sign-up form after the creator left: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/consumer-signup/forecast", api.CreateConsumerSignupRequest{Email: "bob@example.com", Name: "Bob"}, public); res.Code != http.StatusNotFound {
		t.Fatalf("sign-up after the creator left: %d %s", res.Code, res.Body)
	}
	if res := complete(pending); res.Code != http.StatusNotFound {
		t.Fatalf("completing a pending sign-up after the creator left: %d %s", res.Code, res.Body)
	}
	if len(stripe.customers) != customers {
		t.Fatalf("Stripe customers %d after the creator left; want %d", len(stripe.customers), customers)
	}
	if res := e.Do(t, http.MethodPost, "/v1/consumer-billing/forecast/portal", nil, customer); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), api.CodeConsumerSelfServiceUnavailable) {
		t.Fatalf("portal after the creator left: %d %s", res.Code, res.Body)
	}
	ok(t, e.Do(t, http.MethodGet, "/v1/consumer-billing/forecast", nil, customer), http.StatusOK, nil)
}
