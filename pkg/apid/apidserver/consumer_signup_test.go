package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
)

// consumerSignupStripe fakes the Stripe calls of a key check and a consumer
// sign-up. complete reports whether the customer finished the checkout.
type consumerSignupStripe struct {
	mu             sync.Mutex
	complete       bool
	customers      []string
	successURL     string
	defaultPayment string
	portalCustomer string
	portalReturn   string
}

func (f *consumerSignupStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
		f.customers = append(f.customers, r.PostForm.Get("email"))
		_, _ = fmt.Fprintf(w, `{"id":"cus_%d"}`, len(f.customers))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		f.successURL = r.PostForm.Get("success_url")
		_, _ = fmt.Fprintf(w, `{"id":"cs_test_%d","url":"https://checkout.stripe.test/%d","status":"open","customer":%q}`,
			len(f.customers), len(f.customers), r.PostForm.Get("customer"))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		status, intent := "open", `"seti_1"`
		if f.complete {
			status, intent = "complete", `{"id":"seti_1","status":"succeeded","payment_method":"pm_1"}`
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":%q,"customer":"cus_1","setup_intent":%s}`, strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/"), status, intent)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/billing_portal/sessions":
		f.portalCustomer, f.portalReturn = r.PostForm.Get("customer"), r.PostForm.Get("return_url")
		_, _ = fmt.Fprint(w, `{"id":"bps_1","url":"https://billing.stripe.test/session/bps_1"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers/cus_1":
		f.defaultPayment = r.PostForm.Get("invoice_settings[default_payment_method]")
		_, _ = fmt.Fprint(w, `{"id":"cus_1"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":{"message":"no such route"}}`)
	}
}

// adr: 20261021143000 — a customer signs up on the hosted flow: the owner
// sets it up, the customer saves a card with Stripe Checkout and gets a
// key; completing again rotates the key; the daily limit and turning
// sign-up off hold.
func TestConsumerSignup(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &consumerSignupStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	apidserver.MustSeedApp(t, e, "weather")
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

	var plan api.APIConsumerPlanResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/weather/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: "pro", MaxUnitsPerMonth: 100000}, nil),
		http.StatusCreated, &plan)
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/weather/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
		PriceMillicentsPerUnit: 50, PlanID: plan.ID}, nil), http.StatusCreated, nil)
	settings := api.PutConsumerSignupRequest{DisplayName: "Weather API", PlanIDs: []string{plan.ID}, TrialDays: 7,
		ReturnURL: "https://weather.example/welcome", TermsURL: "https://weather.example/terms"}
	if res := e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", settings, nil); res.Code != http.StatusConflict {
		t.Fatalf("sign-up before Stripe is connected: %d %s", res.Code, res.Body)
	}
	decode(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)
	if res := e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", api.PutConsumerSignupRequest{DisplayName: "Weather API",
		Provider: "paddle"}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("paddle sign-up: %d %s", res.Code, res.Body)
	}
	var set api.ConsumerSignupSettingsResponse
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", settings, nil), http.StatusOK, &set)
	if !set.Enabled || set.Provider != "stripe" || set.DailyLimit != api.ConsumerSignupDailyLimitDefault ||
		strings.Join(set.KeyScopes, ",") != "write,billing" || !strings.HasSuffix(set.PageURL, "/v1/consumer-signup/weather/page") {
		t.Fatalf("settings = %+v", set)
	}

	public := map[string]string{"Authorization": ""}
	var form api.ConsumerSignupFormResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/weather", nil, public), http.StatusOK, &form)
	if form.DisplayName != "Weather API" || form.Livemode || form.TrialDays != 7 || len(form.Plans) != 1 || form.Plans[0].ID != plan.ID ||
		form.Plans[0].Name != "pro" || len(form.Plans[0].Prices) != 1 || form.Plans[0].Prices[0].PriceMillicentsPerUnit != 50 {
		t.Fatalf("form = %+v", form)
	}
	page := e.Do(t, http.MethodGet, "/v1/consumer-signup/weather/page", nil, public)
	if page.Code != http.StatusOK || !strings.Contains(page.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("page: %d %v", page.Code, page.Header())
	}

	if res := e.Do(t, http.MethodPost, "/v1/consumer-signup/weather", api.CreateConsumerSignupRequest{Email: "not-an-email", Name: "Ada"}, public); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad email: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/consumer-signup/weather", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada",
		PlanID: "00000000-0000-4000-8000-000000000000"}, public); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plan not offered: %d %s", res.Code, res.Body)
	}
	var started api.CreateConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/weather", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada"}, public),
		http.StatusCreated, &started)
	if started.CheckoutURL != "https://checkout.stripe.test/1" || started.ClaimToken == "" || len(stripe.customers) != 1 ||
		!strings.HasSuffix(stripe.successURL, "/v1/consumer-signup/weather/page?signup="+started.ID) {
		t.Fatalf("started = %+v, success URL %q", started, stripe.successURL)
	}

	completePath := "/v1/consumer-signup/weather/signups/" + started.ID + "/complete"
	if res := e.Do(t, http.MethodPost, completePath, api.CompleteConsumerSignupRequest{ClaimToken: "wrong"}, public); res.Code != http.StatusNotFound {
		t.Fatalf("wrong claim token: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, completePath, api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), api.CodeConsumerSignupCheckoutIncomplete) {
		t.Fatalf("completion before the card is saved: %d %s", res.Code, res.Body)
	}
	stripe.complete = true
	var done api.CompleteConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, completePath, api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public), http.StatusOK, &done)
	if !strings.HasPrefix(done.Key, api.ConsumerKeyPrefix) || done.PlanID != plan.ID || done.TrialEndsAt == nil ||
		done.ReturnURL != "https://weather.example/welcome" || strings.Join(done.Scopes, ",") != "write,billing" || stripe.defaultPayment != "pm_1" {
		t.Fatalf("completed = %+v, default payment %q", done, stripe.defaultPayment)
	}
	var consumers api.APIConsumerListResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/weather/consumers", nil, nil), http.StatusOK, &consumers)
	if len(consumers.Consumers) != 1 || consumers.Consumers[0].ExternalRef != "cus_1" || consumers.Consumers[0].Name != "Ada" ||
		consumers.Consumers[0].ID != done.ConsumerID {
		t.Fatalf("consumers = %+v", consumers)
	}
	billing := func(key string) int {
		return e.Do(t, http.MethodGet, "/v1/consumer-billing/weather", nil, map[string]string{"Authorization": "Bearer " + key}).Code
	}
	if code := billing(done.Key); code != http.StatusOK {
		t.Fatalf("billing with the sign-up key: %d", code)
	}

	var again api.CompleteConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, completePath, api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public), http.StatusOK, &again)
	if again.ConsumerID != done.ConsumerID || again.Key == done.Key {
		t.Fatalf("second completion = %+v", again)
	}
	if code := billing(done.Key); code != http.StatusUnauthorized {
		t.Fatalf("the replaced key still works: %d", code)
	}
	if code := billing(again.Key); code != http.StatusOK {
		t.Fatalf("the new key: %d", code)
	}
	var signups api.ConsumerSignupListResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/weather/consumer-signups", nil, nil), http.StatusOK, &signups)
	if len(signups.Signups) != 1 || signups.Signups[0].Status != "completed" || signups.Signups[0].ConsumerID != done.ConsumerID ||
		signups.Signups[0].Email != "ada@example.com" {
		t.Fatalf("signups = %+v", signups)
	}

	settings.DailyLimit = 1
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", settings, nil), http.StatusOK, nil)
	if res := e.Do(t, http.MethodPost, "/v1/consumer-signup/weather", api.CreateConsumerSignupRequest{Email: "bob@example.com", Name: "Bob"}, public); res.Code != http.StatusTooManyRequests ||
		!strings.Contains(res.Body.String(), api.CodeConsumerSignupDailyLimit) {
		t.Fatalf("past the daily limit: %d %s", res.Code, res.Body)
	}
	decode(t, e.Do(t, http.MethodDelete, "/v1/apps/weather/consumer-signup", nil, nil), http.StatusNoContent, nil)
	if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/weather", nil, public); res.Code != http.StatusNotFound {
		t.Fatalf("form after sign-up was turned off: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/no-such-app", nil, public); res.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d %s", res.Code, res.Body)
	}
}
