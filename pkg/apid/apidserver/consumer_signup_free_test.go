package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261124094745 — a free plan whose rate cards charge nothing is
// joined without a card, and with OAuth consumers on, a verified OAuth caller
// becomes a consumer on it once, however often it calls.
func TestConsumerSignupFreePlanAndOAuthConsumers(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &consumerSignupStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
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

	// Before OAuth consumers are on, enrollment is refused.
	enroll := func(subject string) (state.APIConsumer, bool, error) {
		return (*e.S()).ConsumerSignup().EnrollOAuthConsumer(t.Context(), state.APIConsumerEnrollment{AccountID: e.Acct().ID, AppID: appID,
			Issuer: "https://login.example", Subject: subject, Email: subject + "@example.com"})
	}
	if _, _, err := enroll("u1"); !errors.Is(err, state.ErrAPIConsumerOAuthOff) {
		t.Fatalf("enroll before sign-up = %v; want OAuth off", err)
	}

	settings := api.PutConsumerSignupRequest{DisplayName: "Search tools", PlanIDs: []string{free, pro}}
	for name, bad := range map[string]api.PutConsumerSignupRequest{
		"oauth without a free plan": {DisplayName: "Search tools", PlanIDs: settings.PlanIDs, OAuthConsumers: true},
		"free plan that charges":    {DisplayName: "Search tools", PlanIDs: settings.PlanIDs, FreePlanID: pro},
		"free plan not offered":     {DisplayName: "Search tools", PlanIDs: []string{pro}, FreePlanID: free},
	} {
		if res := e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", bad, nil); res.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	settings.FreePlanID, settings.OAuthConsumers = free, true
	var set api.ConsumerSignupSettingsResponse
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, &set)
	if set.FreePlanID != free || !set.OAuthConsumers {
		t.Fatalf("settings = %+v", set)
	}
	// The free plan stays free: a charging card on it is refused.
	if res := e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
		BaseFeeMillicentsPerMonth: 100, PlanID: free}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("charging card on the free plan: %d %s", res.Code, res.Body)
	}

	public := map[string]string{"Authorization": ""}
	var form api.ConsumerSignupFormResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search", nil, public), http.StatusOK, &form)
	for _, p := range form.Plans {
		if p.Free != (p.ID == free) {
			t.Errorf("plan %s free = %v", p.Name, p.Free)
		}
	}

	// The free plan skips Checkout: no Stripe customer, no checkout URL.
	var started api.CreateConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada",
		PlanID: free}, public), http.StatusCreated, &started)
	if started.CheckoutURL != "" || started.ClaimToken == "" || len(stripe.customers) != 0 {
		t.Fatalf("started = %+v, customers %v", started, stripe.customers)
	}
	var done api.CompleteConsumerSignupResponse
	decode(t, e.Do(t, http.MethodPost, "/v1/consumer-signup/search/signups/"+started.ID+"/complete",
		api.CompleteConsumerSignupRequest{ClaimToken: started.ClaimToken}, public), http.StatusOK, &done)
	if !strings.HasPrefix(done.Key, api.ConsumerKeyPrefix) || done.PlanID != free {
		t.Fatalf("completed = %+v", done)
	}

	consumer, created, err := enroll("u1")
	if err != nil || !created || !consumer.Active() || !strings.HasPrefix(consumer.ExternalRef, "oauth:") || consumer.Name != "u1@example.com" {
		t.Fatalf("enroll = %+v %v %v", consumer, created, err)
	}
	again, created, err := enroll("u1")
	if err != nil || created || again.ID != consumer.ID {
		t.Fatalf("second enroll = %+v %v %v", again, created, err)
	}
	var consumers api.APIConsumerListResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search/consumers", nil, nil), http.StatusOK, &consumers)
	if len(consumers.Consumers) != 2 {
		t.Fatalf("consumers = %+v", consumers)
	}

	// Another account's app is not found.
	if _, _, err := (*e.S()).ConsumerSignup().EnrollOAuthConsumer(t.Context(), state.APIConsumerEnrollment{AccountID: "00000000-0000-4000-8000-000000000001",
		AppID: appID, Issuer: "https://login.example", Subject: "u2"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account = %v; want not found", err)
	}

	// Linking by key: u1 proves Ada's key, so u1 now bills to Ada's
	// consumer and the consumer u1's first sign-in enrolled is closed.
	link := func(subject, key string) (state.APIConsumerIdentity, string, error) {
		return (*e.S()).ConsumerSignup().LinkOAuthConsumer(t.Context(), state.APIConsumerIdentityLink{AccountID: e.Acct().ID, AppID: appID,
			Issuer: "https://login.example", Subject: subject}, key)
	}
	wrongKey := done.Key[:len(done.Key)-1] + "0"
	if wrongKey == done.Key {
		wrongKey = done.Key[:len(done.Key)-1] + "1"
	}
	for name, key := range map[string]string{"malformed": "ck_nope", "wrong secret": wrongKey} {
		if _, _, err := link("u1", key); !errors.Is(err, state.ErrAPIConsumerKeyRejected) {
			t.Errorf("%s key = %v; want rejected", name, err)
		}
	}
	identity, revoked, err := link("u1", done.Key)
	if err != nil || identity.ConsumerID != done.ConsumerID || revoked != consumer.ID {
		t.Fatalf("link = %+v %q %v", identity, revoked, err)
	}
	if again, _, err := enroll("u1"); err != nil || again.ID != done.ConsumerID {
		t.Fatalf("enroll after link = %+v %v; want Ada's consumer", again, err)
	}
	if _, _, err := (*e.S()).ConsumerSignup().LinkOAuthConsumer(t.Context(), state.APIConsumerIdentityLink{
		AccountID: "00000000-0000-4000-8000-000000000001", AppID: appID, Issuer: "https://login.example", Subject: "u1"},
		done.Key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account link = %v; want not found", err)
	}

	t.Run("manage identities with MCP hosting disabled", func(t *testing.T) {
		server := *e.S()
		if err := server.InstallCapabilityGate(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { server.WithCapabilityGate(nil) })
		server.SeedPlatformFlag(capabilityFlag(t, "mcp-hosting"), platformflags.Definition{Enabled: false})

		const path = "/v1/apps/search/consumer-identities"
		decode(t, e.Do(t, http.MethodGet, path, nil, public), http.StatusUnauthorized, nil)
		var ids api.ConsumerIdentityListResponse
		decode(t, e.Do(t, http.MethodGet, path, nil, nil), http.StatusOK, &ids)
		if len(ids.Identities) != 1 || ids.Identities[0].ConsumerID != done.ConsumerID || ids.Identities[0].Subject != "u1" {
			t.Fatalf("identities = %+v", ids)
		}

		unlink := path + "/" + ids.Identities[0].ID
		decode(t, e.Do(t, http.MethodDelete, unlink, nil, public), http.StatusUnauthorized, nil)
		decode(t, e.Do(t, http.MethodDelete, unlink, nil, nil), http.StatusNoContent, nil)
		decode(t, e.Do(t, http.MethodDelete, unlink, nil, nil), http.StatusNotFound, nil)
	})
	if fresh, created, err := enroll("u1"); err != nil || !created || fresh.ID == done.ConsumerID {
		t.Fatalf("enroll after unlink = %+v %v %v; want a new consumer", fresh, created, err)
	}

	// Card-less consumers are never charged: the owner cannot move one to a
	// paid plan, nor price a former free plan they are still on.
	if res := e.Do(t, http.MethodPost, "/v1/apps/search/consumers/"+done.ConsumerID+"/plan-assignments",
		api.AssignAPIConsumerPlanRequest{PlanID: pro}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("card-less consumer onto a paid plan: %d %s", res.Code, res.Body)
	}
	free2 := plan("free2", 0)
	settings.PlanIDs, settings.FreePlanID = []string{free, pro, free2}, free2
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", settings, nil), http.StatusOK, nil)
	if res := e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
		PriceMillicentsPerUnit: 10, PlanID: free}, nil); res.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(res.Body.String(), "joined without a card") {
		t.Fatalf("charging card on a plan with card-less consumers: %d %s", res.Code, res.Body)
	}
}

// A seller can sell an MCP server on the free plan alone before connecting
// Stripe: no sign-up reaches Checkout. Offering a paid plan still needs it.
func TestConsumerSignupFreePlanOnlyNeedsNoStripe(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	appID := apidserver.MustSeedApp(t, e, "search")
	plan := func(name string, price int64) string {
		var p api.APIConsumerPlanResponse
		res := e.Do(t, http.MethodPost, "/v1/apps/search/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: name, MaxUnitsPerMonth: 1000}, nil)
		if res.Code != http.StatusCreated || json.Unmarshal(res.Body.Bytes(), &p) != nil {
			t.Fatalf("plan %s: %d %s", name, res.Code, res.Body)
		}
		if res := e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
			PriceMillicentsPerUnit: price, PlanID: p.ID}, nil); res.Code != http.StatusCreated {
			t.Fatalf("rate card %s: %d %s", name, res.Code, res.Body)
		}
		return p.ID
	}
	free, pro := plan("free", 0), plan("pro", 50)
	put := func(plans ...string) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", api.PutConsumerSignupRequest{DisplayName: "Search tools",
			PlanIDs: plans, FreePlanID: free, OAuthConsumers: true, AccountTools: true}, nil)
	}
	if res := put(free, pro); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "Stripe is not connected") {
		t.Fatalf("paid plan offered without Stripe: %d %s", res.Code, res.Body)
	}
	if res := put(free); res.Code != http.StatusOK {
		t.Fatalf("free plan only without Stripe: %d %s", res.Code, res.Body)
	}
	if _, created, err := (*e.S()).ConsumerSignup().EnrollOAuthConsumer(t.Context(), state.APIConsumerEnrollment{AccountID: e.Acct().ID, AppID: appID,
		Issuer: "https://login.example", Subject: "u1", Email: "u1@example.com"}); err != nil || !created {
		t.Fatalf("enroll without Stripe: created=%v err=%v", created, err)
	}
	// Adding a paid plan later still asks for Stripe.
	if res := put(free, pro); res.Code != http.StatusConflict {
		t.Fatalf("adding a paid plan without Stripe: %d %s", res.Code, res.Body)
	}
}
