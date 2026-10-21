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

// adr: 20261021183000 — a signed-up customer lists plans, switches plans,
// opens the Stripe portal and closes their account with their billing key,
// as far as the owner allows, and the owner's webhook hears about the
// sign-up, the plan change and the close.
func TestConsumerSelfService(t *testing.T) {
	withBillingKeys(t)
	e := setupWebhookTest(t, api.PlanPro)
	stripe := &signupStripe{complete: true}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	(*e.S()).ConsumerBilling().Stripe = client
	appID := apidserver.MustSeedApp(t, e, "weather")
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
	plan := func(name string) api.APIConsumerPlanResponse {
		var p api.APIConsumerPlanResponse
		ok(t, e.Do(t, http.MethodPost, "/v1/apps/weather/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: name}, nil), http.StatusCreated, &p)
		ok(t, e.Do(t, http.MethodPost, "/v1/apps/weather/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
			PriceMillicentsPerUnit: 50, PlanID: p.ID}, nil), http.StatusCreated, nil)
		return p
	}
	pro, team := plan("pro"), plan("team")
	hook := mustCreateWebhook(t, e, "weather", api.CreateAppWebhookRequest{TargetURL: "https://owner.example/hook", WebhookSecret: "shh-test",
		EventFilter: []string{"consumer.signed_up", "consumer.plan_changed", "consumer.closed"}})
	ok(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)
	settings := api.PutConsumerSignupRequest{DisplayName: "Weather API", PlanIDs: []string{pro.ID, team.ID}, ReturnURL: "https://weather.example/account",
		CustomerPlanChanges: true}
	var set api.ConsumerSignupSettingsResponse
	ok(t, e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", settings, nil), http.StatusOK, &set)
	if !set.CustomerPlanChanges || set.CustomerClosing {
		t.Fatalf("settings = %+v", set)
	}

	public := map[string]string{"Authorization": ""}
	var started api.CreateConsumerSignupResponse
	ok(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/weather", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada",
		PlanID: pro.ID}, public), http.StatusCreated, &started)
	var done api.CompleteConsumerSignupResponse
	ok(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/weather/signups/"+started.ID+"/complete",
		api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public), http.StatusOK, &done)
	customer := map[string]string{"Authorization": "Bearer " + done.Key}

	var plans api.ConsumerBillingPlansResponse
	ok(t, e.Do(t, http.MethodGet, "/v1/consumer-billing/weather/plans", nil, customer), http.StatusOK, &plans)
	if plans.CurrentPlanID != pro.ID || !plans.CanChange || plans.CanClose || len(plans.Plans) != 2 {
		t.Fatalf("plans = %+v", plans)
	}
	if res := e.Do(t, http.MethodPut, "/v1/consumer-billing/weather/plan", api.ChangeConsumerBillingPlanRequest{PlanID: pro.ID}, customer); res.Code != http.StatusConflict {
		t.Fatalf("switch to the current plan: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPut, "/v1/consumer-billing/weather/plan", api.ChangeConsumerBillingPlanRequest{PlanID: "00000000-0000-4000-8000-000000000000"}, customer); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("switch to a plan not offered: %d %s", res.Code, res.Body)
	}
	var changed api.ConsumerBillingPlanChangeResponse
	ok(t, e.Do(t, http.MethodPut, "/v1/consumer-billing/weather/plan", api.ChangeConsumerBillingPlanRequest{PlanID: team.ID}, customer), http.StatusOK, &changed)
	if changed.PlanID != team.ID || changed.EffectiveFrom.IsZero() {
		t.Fatalf("plan change = %+v", changed)
	}
	if res := e.Do(t, http.MethodPut, "/v1/consumer-billing/weather/plan", api.ChangeConsumerBillingPlanRequest{PlanID: team.ID}, customer); res.Code != http.StatusConflict {
		t.Fatalf("second change in the same minute: %d %s", res.Code, res.Body)
	}

	var portal api.ConsumerBillingPortalResponse
	ok(t, e.Do(t, http.MethodPost, "/v1/consumer-billing/weather/portal", nil, customer), http.StatusOK, &portal)
	if portal.URL != "https://billing.stripe.test/session/bps_1" || stripe.portalCustomer != "cus_1" || stripe.portalReturn != "https://weather.example/account" {
		t.Fatalf("portal = %+v, customer %q, return %q", portal, stripe.portalCustomer, stripe.portalReturn)
	}

	if res := e.Do(t, http.MethodPost, "/v1/consumer-billing/weather/close", nil, customer); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), api.CodeConsumerSelfServiceUnavailable) {
		t.Fatalf("close before the owner allows it: %d %s", res.Code, res.Body)
	}
	settings.CustomerClosing = true
	ok(t, e.Do(t, http.MethodPut, "/v1/apps/weather/consumer-signup", settings, nil), http.StatusOK, nil)
	var closed api.ConsumerBillingCloseResponse
	ok(t, e.Do(t, http.MethodPost, "/v1/consumer-billing/weather/close", nil, customer), http.StatusOK, &closed)
	if closed.ConsumerID != done.ConsumerID || closed.ClosedAt.IsZero() {
		t.Fatalf("closed = %+v", closed)
	}
	if res := e.Do(t, http.MethodGet, "/v1/consumer-billing/weather", nil, customer); res.Code != http.StatusUnauthorized {
		t.Fatalf("key after closing: %d %s", res.Code, res.Body)
	}

	deliveries, _, err := (*e.Store()).ListAppWebhookDeliveries(t.Context(), appID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	events := map[state.AppWebhookEvent]bool{}
	for _, d := range deliveries {
		events[d.Event] = true
	}
	if len(deliveries) != 3 || !events[state.AppWebhookEventConsumerSignedUp] || !events[state.AppWebhookEventConsumerPlanChanged] ||
		!events[state.AppWebhookEventConsumerClosed] {
		t.Fatalf("owner webhook deliveries = %+v", deliveries)
	}
}
