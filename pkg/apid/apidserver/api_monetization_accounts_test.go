package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// adr: 20261020170649 — with the runtime switch off, the api_monetization
// platform flag releases monetization to the accounts it lists. Once any
// account has it the routes reach authentication; an account outside the
// flag gets the unregistered-route answer and no monetization webhook
// events, a listed one gets all of it, and the served spec still omits it
// until every account is included.
func TestAPIMonetizationReleasedPerAccount(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "0")
	e := setupWebhookTest(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	hook := api.CreateAppWebhookRequest{TargetURL: "https://example.com/hook", WebhookSecret: "shh-test",
		EventFilter: []string{"usage_statement.finalized"}}

	(*e.S()).SeedPlatformFlag(platformflags.APIMonetization, platformflags.FromAccountList(true, false, []string{uuid.NewString()}))
	res := e.Do(t, http.MethodGet, "/v1/apps/shop/rate-cards", nil, nil)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "no such API route") || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("account outside the flag: %d %s", res.Code, res.Body)
	}
	unauthenticated := httptest.NewRecorder()
	(*e.H()).ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/v1/apps/shop/rate-cards", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated while some accounts have it: %d %s, want 401", unauthenticated.Code, unauthenticated.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/apps/shop/webhooks", hook, nil); res.Code == http.StatusCreated {
		t.Fatalf("monetization event offered outside the flag: %s", res.Body)
	}
	// The public sign-up routes resolve the account from the app
	// (ADR-20261021143000).
	signupForm := httptest.NewRecorder()
	(*e.H()).ServeHTTP(signupForm, httptest.NewRequest(http.MethodGet, "/v1/consumer-signup/shop", nil))
	if signupForm.Code != http.StatusNotFound || !strings.Contains(signupForm.Body.String(), "no such API route") {
		t.Fatalf("sign-up form of an account outside the flag: %d %s", signupForm.Code, signupForm.Body)
	}
	if res := e.Do(t, http.MethodGet, "/v1/apps/shop/consumer-signup", nil, nil); res.Code != http.StatusNotFound ||
		!strings.Contains(res.Body.String(), "no such API route") {
		t.Fatalf("sign-up settings outside the flag: %d %s", res.Code, res.Body)
	}

	(*e.S()).SeedPlatformFlag(platformflags.APIMonetization, platformflags.FromAccountList(true, false, []string{(*e.Acct()).ID}))
	if res := e.Do(t, http.MethodGet, "/v1/apps/shop/rate-cards", nil, nil); res.Code != http.StatusOK {
		t.Fatalf("listed account rate cards: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/apps/shop/webhooks", hook, nil); res.Code != http.StatusCreated {
		t.Fatalf("listed account statement webhook: %d %s", res.Code, res.Body)
	}
	trial := api.CreateAPIConsumerRequest{ExternalRef: "c1", Name: "C1", TrialDays: 7}
	if res := e.Do(t, http.MethodPost, "/v1/apps/shop/consumers", trial, nil); res.Code != http.StatusCreated {
		t.Fatalf("listed account trial on create: %d %s", res.Code, res.Body)
	}
	if servedPaths(t, e)["/v1/apps/{slug}/rate-cards"] {
		t.Fatal("served spec shows monetization before every account has it")
	}

	(*e.S()).SeedPlatformFlag(platformflags.APIMonetization, platformflags.FromAccountList(true, true, nil))
	if !servedPaths(t, e)["/v1/apps/{slug}/rate-cards"] {
		t.Fatal("served spec omits monetization once every account has it")
	}
}
