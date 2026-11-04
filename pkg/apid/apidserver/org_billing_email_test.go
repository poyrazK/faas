// adr: 20261103131742 — Polar keeps one customer per email: invoices and the
// billing portal follow the organization's billing email, a deleted subject's
// customer gives its email back, and a subscriber upgrades through the API.
package apidserver_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

type identityCustomer struct{ email, external string }

// identityPolar is a Polar API that keeps one customer per email.
type identityPolar struct {
	mu            sync.Mutex
	customers     map[string]identityCustomer // customer ID -> customer
	patches       int
	deleted       []string
	declineCharge bool
	prorations    []string
}

const identityEmailTaken = `{"detail":[{"loc":["body","email"],"msg":"A customer with this email address already exists."}]}`

func (f *identityPolar) holder(email string) (string, bool) {
	for id, c := range f.customers {
		if c.email == email {
			return id, true
		}
	}
	return "", false
}

func (f *identityPolar) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email      string `json:"email"`
		ExternalID string `json:"external_id"`
		Proration  string `json:"proration_behavior"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/customers/")
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/customers/external/"):
		for cid, c := range f.customers {
			if c.external == strings.TrimPrefix(r.URL.Path, "/v1/customers/external/") {
				_, _ = io.WriteString(w, `{"id":"`+cid+`"}`)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/":
		if cid, ok := f.holder(r.URL.Query().Get("email")); ok {
			_, _ = io.WriteString(w, `{"items":[{"id":"`+cid+`","external_id":"`+f.customers[cid].external+`"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
		if _, taken := f.holder(body.Email); taken {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, identityEmailTaken)
			return
		}
		f.customers["cus-"+body.ExternalID] = identityCustomer{email: body.Email, external: body.ExternalID}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"cus-`+body.ExternalID+`"}`)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/customers/"):
		if holder, taken := f.holder(body.Email); taken && holder != id {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, identityEmailTaken)
			return
		}
		f.patches++
		c := f.customers[id]
		c.email = body.Email
		f.customers[id] = c
		_, _ = io.WriteString(w, `{"id":"`+id+`"}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/customers/"):
		f.deleted = append(f.deleted, id)
		delete(f.customers, id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/subscriptions/"):
		f.prorations = append(f.prorations, body.Proration)
		if f.declineCharge {
			// A gateway error would be retried by an ordinary call; a
			// charge must not be.
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":"PaymentFailed","detail":"card declined"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"sub-1","status":"active","current_period_end":"2026-12-01T00:00:00Z"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customer-sessions":
		_, _ = io.WriteString(w, `{"customer_portal_url":"https://polar.example/portal/session"}`)
	default:
		t.Errorf("unexpected Polar call %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

// identityProvider wires a Polar provider backed by fake into e's server.
func identityProvider(t *testing.T, e apidserver.TestEnv, fake *identityPolar) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.handle(t, w, r)
	}))
	t.Cleanup(srv.Close)
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-identity-test-token", WebhookSecret: freshnessWebhookSecret,
		BaseURL: srv.URL, HobbyProductID: "hobby-product", ProProductID: "pro-product", ScaleProductID: "scale-product"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithBillingProvider(provider)
}

func (f *identityPolar) email(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.customers[id].email
}

// An organization's first checkout bills the member who started it. The owner
// moves the billing email to the organization's own address, the Polar
// customer moves with it, and the member who held it cannot be removed while
// invoices and the portal sign-in still go to them.
func TestOrgBillingEmailFollowsTheOrganization(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	fake := &identityPolar{customers: map[string]identityCustomer{"cus-other": {email: "taken@example.com", external: e.Acct().ID}}}
	identityProvider(t, e, fake)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "identity-co", "Identity Co", api.PlanPro)
	member, viewer := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	put := func(email string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		return e.Do(t, http.MethodPut, "/v1/orgs/"+org.Slug+"/billing/email", api.OrgBillingEmailRequest{BillingEmail: email}, headers)
	}
	billingEmail := func() string {
		t.Helper()
		subject, err := state.BillingResourceAccount(ctx, store, org.ID)
		if err != nil {
			t.Fatal(err)
		}
		return subject.Email
	}

	// Before any checkout there is no customer: the email is only saved.
	if rec := put(member.Email, nil); rec.Code != http.StatusOK {
		t.Fatalf("set billing email before checkout: %d %s", rec.Code, rec.Body)
	}
	if fake.patches != 0 || billingEmail() != member.Email {
		t.Fatalf("patches = %d, billing email = %q; want 0, %q", fake.patches, billingEmail(), member.Email)
	}

	fake.customers["cus-org"] = identityCustomer{email: member.Email, external: org.ID}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: "cus-org", SubscriptionID: "sub-org"}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+member.ID, nil, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeBillingContactMember) {
		t.Fatalf("remove the billing contact: %d %s, want 409 %s", rec.Code, rec.Body, api.CodeBillingContactMember)
	}
	if p := (*e.S()).BillingContactBlocksDeletionForTest(ctx, member); p == nil || p.Code != api.CodeBillingContactMember {
		t.Fatalf("billing contact deleting their account: %+v, want %s", p, api.CodeBillingContactMember)
	}

	for _, tc := range []struct {
		email   string
		headers map[string]string
		code    int
	}{
		{"not-an-email", nil, http.StatusBadRequest},
		{"taken@example.com", nil, http.StatusConflict},
		{"finance@example.com", viewer, http.StatusForbidden},
	} {
		if rec := put(tc.email, tc.headers); rec.Code != tc.code {
			t.Fatalf("PUT billing email %q: %d %s, want %d", tc.email, rec.Code, rec.Body, tc.code)
		}
	}
	if billingEmail() != member.Email || fake.email("cus-org") != member.Email {
		t.Fatalf("a refused change moved the email: org %q, customer %q", billingEmail(), fake.email("cus-org"))
	}

	rec = put(" Finance@Example.com ", nil)
	var out api.OrgBillingEmailResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.BillingEmail != "finance@example.com" {
		t.Fatalf("move billing email: %d %s", rec.Code, rec.Body)
	}
	if billingEmail() != "finance@example.com" || fake.email("cus-org") != "finance@example.com" {
		t.Fatalf("after the move: org %q, customer %q; want finance@example.com", billingEmail(), fake.email("cus-org"))
	}
	if p := (*e.S()).BillingContactBlocksDeletionForTest(ctx, member); p != nil {
		t.Fatalf("former billing contact deleting their account: %+v, want allowed", p)
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+member.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove the former billing contact: %d %s", rec.Code, rec.Body)
	}
}

// A deleted account's Polar customer keeps its email, so a returning customer
// could never pay again. That customer is deleted and replaced; a customer of
// a live account or organization never is.
func TestLeftoverCustomerOfADeletedSubjectIsReplaced(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	returning, err := store.CreateAccount(ctx, "returning@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	fake := &identityPolar{customers: map[string]identityCustomer{
		"cus-gone": {email: "returning@example.com", external: "0f6f3f53-8d9b-4a9e-9a43-6a3c1b7d2e10"},
		"cus-live": {email: e.Acct().Email, external: e.Acct().ID},
	}}
	identityProvider(t, e, fake)

	got, err := (*e.S()).CreateProviderCustomerForTest(ctx, returning)
	if err != nil || got != "cus-"+returning.ID {
		t.Fatalf("returning customer = %q, %v; want cus-%s", got, err, returning.ID)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "cus-gone" {
		t.Fatalf("deleted = %v, want [cus-gone]", fake.deleted)
	}

	org := apidserver.SeedSharedOrgWithOwner(t, e, "shares-email", "Shares Email", api.PlanFree)
	orgSubject, err := state.BillingResourceAccount(ctx, store, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	orgSubject.Email = e.Acct().Email
	_, err = (*e.S()).CreateProviderCustomerForTest(ctx, orgSubject)
	if !errors.Is(err, billing.ErrCustomerEmailInUse) || len(fake.deleted) != 1 {
		t.Fatalf("organization billing to its owner's email: %v, deleted %v; want ErrCustomerEmailInUse and no deletion", err, fake.deleted)
	}
}

// The customer portal no longer switches plans, so a subscriber upgrades
// through the API: Polar charges the prorated difference now and applies the
// upgrade only when the charge succeeds. A declined charge leaves the plan
// and is sent once; the customer's retry is a new charge.
func TestSubscriberUpgradeIsChargedNow(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanHobby)
	fake := &identityPolar{customers: map[string]identityCustomer{}, declineCharge: true}
	identityProvider(t, e, fake)
	store := *e.Store()
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: e.Acct().ID, Provider: "polar", CustomerID: "cus-a", SubscriptionID: "sub-a"}); err != nil {
		t.Fatal(err)
	}
	upgrade := func() *httptest.ResponseRecorder {
		t.Helper()
		return e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "pro"}, nil)
	}
	if rec := upgrade(); rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "polar.example/portal/session") {
		t.Fatalf("declined upgrade: %d %s, want 402 with the portal to update the card", rec.Code, rec.Body)
	}
	fake.declineCharge = false
	rec := upgrade()
	var out api.AccountResponse
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.PlanChangeStatus != "pending_provider_confirmation" || out.RequestedPlan != "pro" {
		t.Fatalf("upgrade: %d %s, want 202 pending pro", rec.Code, rec.Body)
	}
	// One attempt per request: the failed charge was not retried, and the
	// customer's retry reached Polar as a new charge.
	if len(fake.prorations) != 2 || fake.prorations[0] != "invoice" || fake.prorations[1] != "invoice" {
		t.Fatalf("prorations = %v, want [invoice invoice]", fake.prorations)
	}
	if acct, err := store.AccountByID(ctx, e.Acct().ID); err != nil || acct.Plan != api.PlanHobby {
		t.Fatalf("plan = %q, %v; want hobby until Polar's webhook confirms", acct.Plan, err)
	}
}
