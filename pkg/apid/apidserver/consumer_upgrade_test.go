package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261124094745 — a free-plan customer cannot switch itself to a paid
// plan without a card; its upgrade link saves one with Stripe Checkout,
// attaches the Stripe customer and changes the plan.
func TestConsumerUpgradeLink(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &consumerSignupStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	signup := (*e.S()).ConsumerSignup()
	signup.Stripe = client
	signup.UpgradeKey = bytes.Repeat([]byte{7}, 32)
	appID := apidserver.MustSeedApp(t, e, "search")
	decode := func(t *testing.T, res *httptest.ResponseRecorder, want int, out any) {
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
	plan := func(name string, price int64) string {
		var p api.APIConsumerPlanResponse
		decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: name, MaxUnitsPerMonth: 1000}, nil),
			http.StatusCreated, &p)
		decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
			PriceMillicentsPerUnit: price, PlanID: p.ID}, nil), http.StatusCreated, nil)
		return p.ID
	}
	free, pro := plan("free", 0), plan("pro", 50)
	decode(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)
	settings := api.PutConsumerSignupRequest{DisplayName: "Search tools", PlanIDs: []string{free, pro}, FreePlanID: free}
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, nil)

	public := map[string]string{"Authorization": ""}
	var started api.CreateConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada",
		PlanID: free}, public), http.StatusCreated, &started)
	var done api.CompleteConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search/signups/"+started.ID+"/complete",
		api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public), http.StatusOK, &done)
	acctID := e.Acct().ID

	// Without --customer-plan-changes there is no link.
	if link, _, err := signup.UpgradeLink(t.Context(), acctID, appID, done.ConsumerID); err != nil || link != "" {
		t.Fatalf("link without plan changes = %q %v", link, err)
	}
	settings.CustomerPlanChanges = true
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, nil)

	// The customer's own key cannot move it to a paid plan without a card.
	keyed := map[string]string{"Authorization": "Bearer " + done.Key}
	if res := e.Do(t, http.MethodPut, "/v1/consumer-billing/search/plan", api.ChangeConsumerBillingPlanRequest{PlanID: pro}, keyed); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), "saved card") {
		t.Fatalf("card-less switch to a paid plan: %d %s", res.Code, res.Body)
	}

	link, _, err := signup.UpgradeLink(t.Context(), acctID, appID, done.ConsumerID)
	if err != nil || !strings.Contains(link, "/v1/consumer-signup/search/upgrade?token=") {
		t.Fatalf("link = %q %v", link, err)
	}
	parsed, _ := url.Parse(link)
	token := parsed.Query().Get("token")
	if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/search/upgrade/plans?token="+url.QueryEscape(token+"x"), nil, public); res.Code != http.StatusNotFound {
		t.Fatalf("forged token: %d %s", res.Code, res.Body)
	}
	var view api.ConsumerUpgradeResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search/upgrade/plans?token="+url.QueryEscape(token), nil, public), http.StatusOK, &view)
	if view.CurrentPlanID != free || view.HasCard || len(view.Plans) != 2 {
		t.Fatalf("view = %+v", view)
	}
	page := e.Do(t, http.MethodGet, "/v1/consumer-signup/search/upgrade?token="+url.QueryEscape(token), nil, public)
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page: %d", page.Code)
	}

	var start api.ConsumerUpgradeResult
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search/upgrade", api.StartConsumerUpgradeRequest{Token: token, PlanID: pro}, public),
		http.StatusOK, &start)
	if start.CheckoutURL == "" || start.CompleteToken == "" || len(stripe.customers) != 1 ||
		!strings.Contains(stripe.successURL, "session={CHECKOUT_SESSION_ID}") {
		t.Fatalf("start = %+v, customers %v, success %q", start, stripe.customers, stripe.successURL)
	}
	complete := api.CompleteConsumerUpgradeRequest{Token: token, CompleteToken: start.CompleteToken, CheckoutSessionID: "cs_test_1"}
	if res := e.Do(t, http.MethodPost, "/v1/consumer-signup/search/upgrade/complete", complete, public); res.Code != http.StatusConflict {
		t.Fatalf("completion before the card is saved: %d %s", res.Code, res.Body)
	}
	stripe.complete = true
	var finished api.ConsumerUpgradeResult
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search/upgrade/complete", complete, public), http.StatusOK, &finished)
	if finished.PlanID != pro || finished.EffectiveFrom == nil || stripe.defaultPayment != "pm_1" {
		t.Fatalf("finished = %+v, default payment %q", finished, stripe.defaultPayment)
	}
	consumer, err := (*e.Store()).GetAPIConsumerByID(t.Context(), acctID, done.ConsumerID)
	if err != nil || consumer.ExternalRef != "cus_1" {
		t.Fatalf("consumer = %+v %v", consumer, err)
	}
	if state.APIConsumerCardless(consumer.ExternalRef) {
		t.Fatal("consumer still card-less")
	}
}
