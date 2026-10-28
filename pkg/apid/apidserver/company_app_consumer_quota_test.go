package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app's consumers and keys keep its creator's account_id, but the
// per-account key allowance belongs to the app's billing subject
// (ADR-202610171904). Once its company adopted billing, a company's keys
// count across all of its apps, whoever created them, and never spend the
// creator's personal allowance. Until then the company keeps its existing
// policy: its creator's company keys share the creator's allowance.
func TestCompanyConsumerKeysShareTheBillingSubjectsAllowance(t *testing.T) {
	for _, adopted := range []bool{true, false} {
		t.Run(fmt.Sprintf("adopted=%v", adopted), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanHobby)
			ctx, store, creator := t.Context(), *e.Store(), *e.Acct()
			// A Free organization starts with billing adopted, and keeps it on
			// an upgrade; a paid one awaits adoption.
			plan := api.PlanHobby
			if adopted {
				plan = api.PlanFree
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "keys-co", Name: "Keys Co", Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			member, err := store.CreateAccount(ctx, "member@keys.example", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, member.ID: state.OrgRoleDeveloper} {
				if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
					t.Fatal(err)
				}
			}
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateOrgAPIKey(ctx, org.ID, creator.ID, hash, "owner", api.ScopesAdminOnly, nil); err != nil {
				t.Fatal(err)
			}
			asOwner := map[string]string{"Authorization": "Bearer " + plain}

			newApp := func(accountID, orgID, slug string) state.App {
				t.Helper()
				app, err := store.CreateApp(ctx, state.App{AccountID: accountID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp,
					Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				return app
			}
			// withKeys gives app a consumer holding n unrevoked keys.
			withKeys := func(app state.App, n int) string {
				t.Helper()
				consumer, err := store.CreateAPIConsumer(ctx, app.AccountID, app.ID, "ref-"+app.Slug, app.Slug)
				if err != nil {
					t.Fatal(err)
				}
				for i := range n {
					_, prefix, hash, err := api.GenerateConsumerKey()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.CreateConsumerKeyForConsumer(ctx, app.AccountID, consumer.ID, fmt.Sprintf("key-%d", i), prefix, hash,
						[]string{"read"}, nil); err != nil {
						t.Fatal(err)
					}
				}
				return consumer.ID
			}
			personal := withKeys(newApp(creator.ID, "", "personal-api"), 0)
			perAccount, perApp := api.PlanHobby.ConsumerKeysPerAccount(), api.PlanHobby.ConsumerKeysPerApp()
			for i, left := 0, perAccount; left > 0; i, left = i+1, left-perApp {
				withKeys(newApp(creator.ID, org.ID, fmt.Sprintf("company-api-%d", i)), min(left, perApp))
			}
			members := withKeys(newApp(member.ID, org.ID, "member-api"), 0)

			issue := func(slug, consumerID string, headers map[string]string) *httptest.ResponseRecorder {
				return e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/consumers/"+consumerID+"/keys",
					api.CreateConsumerKeyRequest{Name: "new", Scopes: []string{"read"}}, headers)
			}
			refused := func(res *httptest.ResponseRecorder) bool {
				return res.Code == http.StatusForbidden && strings.Contains(res.Body.String(), api.CodePlanConsumerKeyQuotaReached) &&
					strings.Contains(res.Body.String(), "per this account")
			}
			// Adopted: the company holds its whole allowance, the creator's is
			// untouched. Unadopted: the creator holds theirs, the member's is untouched.
			if res := issue("personal-api", personal, nil); refused(res) != !adopted || !adopted == (res.Code == http.StatusCreated) {
				t.Fatalf("personal key while the creator's company apps hold %d keys: %d %s", perAccount, res.Code, res.Body)
			}
			if res := issue("member-api", members, asOwner); refused(res) != adopted || adopted == (res.Code == http.StatusCreated) {
				t.Fatalf("key on another member's company app: %d %s", res.Code, res.Body)
			}
		})
	}
}

// Public sign-up issues a company app's keys under its company's plan, not
// its creator's: once the company's plan stops including API consumers, new
// and pending sign-ups get no key although the creator's own plan would.
func TestCompanyAppSignupFollowsTheCompanysPlan(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanPro)
	stripe := &consumerSignupStripe{complete: true}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	ctx, store, creator := t.Context(), *e.Store(), *e.Acct()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "forecast-co", Name: "Forecast Co", Plan: api.PlanPro, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "forecast", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]any{
		"/v1/account/billing-connections/stripe": api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"},
		"/v1/apps/forecast/consumer-signup":      api.PutConsumerSignupRequest{DisplayName: "Forecast API"},
	} {
		if res := e.Do(t, http.MethodPut, path, body, nil); res.Code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", path, res.Code, res.Body)
		}
	}
	public := map[string]string{"Authorization": ""}
	signup := func() *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/consumer-signup/forecast", api.CreateConsumerSignupRequest{Email: "ada@example.com", Name: "Ada"}, public)
	}
	started := signup()
	if started.Code != http.StatusCreated {
		t.Fatalf("sign-up on the company's Pro plan: %d %s", started.Code, started.Body)
	}
	var pending api.CreateConsumerSignupResponse
	if err := json.Unmarshal(started.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if res := signup(); res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), api.CodeConsumerSignupUnavailable) {
		t.Fatalf("sign-up on the company's Free plan: %d %s", res.Code, res.Body)
	}
	res := e.Do(t, http.MethodPost, "/v1/consumer-signup/forecast/signups/"+pending.ID+"/complete",
		api.CompleteConsumerSignupRequest{ClaimToken: pending.ClaimToken}, public)
	if res.Code != http.StatusServiceUnavailable || strings.Contains(res.Body.String(), `"key"`) {
		t.Fatalf("completing a sign-up on the company's Free plan: %d %s", res.Code, res.Body)
	}
}
