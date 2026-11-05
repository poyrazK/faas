package product

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

// signupAuth is an Application Auth app whose user can create organizations.
type signupAuth struct {
	mu      sync.Mutex
	orgs    []api.ProductOnboardingWorkspace
	creates int
}

func (a *signupAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/auth/v1/token" {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "signup.access.jwt", "refresh_token": "signup-refresh", "expires_in": 600})
		return
	}
	if r.Header.Get("Authorization") != "Bearer signup.access.jwt" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /auth/v1/user":
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "user-1", "email": "ada@example.com"})
	case "GET /authz/v1/orgs":
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": a.orgs})
	case "POST /authz/v1/orgs":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		a.creates++
		org := api.ProductOnboardingWorkspace{ID: uuid.NewString(), Slug: body["slug"], Name: body["name"], Role: "owner"}
		a.orgs = append(a.orgs, org)
		_ = json.NewEncoder(w).Encode(org)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type signupFixture struct {
	t        *testing.T
	store    *state.MemStore
	account  state.Account
	app      state.App
	auth     *signupAuth
	host     *billingDetailsTestHost
	service  *Service
	enabled  bool
	cookies  []*http.Cookie
	provider func(email string) (string, *api.Problem)
	keys     []string
}

func newSignupFixture(t *testing.T, policy *api.ProductSignupPolicy) *signupFixture {
	t.Helper()
	ctx := t.Context()
	f := &signupFixture{t: t, store: state.NewMemStore(), auth: &signupAuth{orgs: []api.ProductOnboardingWorkspace{}}, enabled: true}
	var err error
	if f.account, err = f.store.CreateAccount(ctx, "signup-owner@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err = f.store.MarkAccountEmailVerified(ctx, f.account.ID); err != nil {
		t.Fatal(err)
	}
	if f.app, err = f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "signup-product"}); err != nil {
		t.Fatal(err)
	}
	authApp, err := f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "signup-auth"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: authApp.Slug, Currency: "EUR", Signup: policy,
		Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}},
			"pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}}}}
	if _, err := f.store.PutProductCatalog(ctx, f.account.ID, f.app.ID, spec); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(f.auth)
	t.Cleanup(upstream.Close)
	f.host = &billingDetailsTestHost{customerTestHost: customerTestHost{store: f.store}}
	f.provider = func(string) (string, *api.Problem) { return "cus_signup1", nil }
	f.service = &Service{Host: f.host, Enabled: true, EmailOrigin: "https://gregale.example", AuthClient: upstream.Client(),
		AuthOrigin: func(_ context.Context, owner state.Account, slug string) (state.App, string, error) {
			if owner.ID != f.account.ID || slug != authApp.Slug {
				return state.App{}, "", state.ErrNotFound
			}
			return authApp, upstream.URL, nil
		},
		SignupEnabled: func() bool { return f.enabled },
		SignupCustomer: func(_ context.Context, acct state.Account, provider, key, email, name string, metadata map[string]string) (string, *api.Problem) {
			if acct.ID != f.account.ID || provider != policy.Provider || name == "" || metadata["gregale_app"] != f.app.Slug || metadata["gregale_organization"] == "" {
				t.Errorf("provider customer request: %s %s %s %v", acct.ID, provider, name, metadata)
			}
			f.keys = append(f.keys, key)
			return f.provider(email)
		}}
	login := f.call("POST", "/session", `{"email":"ada@example.com","password":"signup-password"}`, f.service.CustomerLogin)
	if login.Code != http.StatusOK {
		t.Fatalf("customer login: %d %s", login.Code, login.Body)
	}
	f.cookies = login.Result().Cookies()
	return f
}

func (f *signupFixture) call(method, path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, "https://portal.example/v1/product-control/customer/"+f.app.Slug+path, strings.NewReader(body))
	r.SetPathValue("slug", f.app.Slug)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://portal.example")
	for _, c := range f.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.service.Gate(handler)(w, r)
	return w
}

func (f *signupFixture) signup(id, plan, name string, status int) api.ProductSignupReceipt {
	f.t.Helper()
	body, _ := json.Marshal(api.ProductSignupRequest{ID: id, Plan: plan, WorkspaceName: name})
	w := f.call("POST", "/signup", string(body), f.service.CustomerSignup)
	if w.Code != status {
		f.t.Fatalf("signup %s %s %q: %d want %d: %s", id, plan, name, w.Code, status, w.Body)
	}
	if strings.Contains(w.Body.String(), "signup.access.jwt") {
		f.t.Fatal("Auth token leaked into the response")
	}
	var receipt api.ProductSignupReceipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	return receipt
}

// adr: 20261022100735 — the public offer exists only while the catalog's
// signup policy and the operator switch are both on, and it shows the
// offered plans with the Auth app's sign-up page returning to the portal.
func TestProductSignupOffer(t *testing.T) {
	f := newSignupFixture(t, &api.ProductSignupPolicy{Plans: []string{"starter"}, Provider: "stripe"})
	w := f.call("GET", "/offer", "", f.service.CustomerSignupOffer)
	var offer api.ProductSignupOffer
	if err := json.Unmarshal(w.Body.Bytes(), &offer); err != nil || w.Code != http.StatusOK || offer.Product != f.app.Slug || offer.Currency != "EUR" || offer.Provider != "stripe" ||
		len(offer.Plans) != 1 || offer.Plans[0].Plan != "starter" || len(offer.Plans[0].Features) != 1 || offer.Plans[0].Features[0].UnitPriceMillicents != 25000 {
		t.Fatalf("offer: %d %+v %v", w.Code, offer, err)
	}
	signUp, err := url.Parse(offer.SignUpURL)
	if err != nil || signUp.Scheme != "https" || signUp.Path != "/ui/sign-up" ||
		signUp.Query().Get("redirect_to") != "https://gregale.example/v1/product-control/portal?app="+f.app.Slug {
		t.Fatalf("sign-up page = %q %v", offer.SignUpURL, err)
	}
	if strings.Contains(w.Body.String(), f.account.ID) || strings.Contains(w.Body.String(), "signup-owner") {
		t.Fatalf("offer names the account: %s", w.Body)
	}
	f.enabled = false
	if w := f.call("GET", "/offer", "", f.service.CustomerSignupOffer); w.Code != http.StatusNotFound {
		t.Fatalf("offer with signup switched off: %d", w.Code)
	}
	if w := f.call("POST", "/signup", `{"id":"`+uuid.NewString()+`","plan":"starter","workspace_name":"Acme"}`, f.service.CustomerSignup); w.Code != http.StatusNotFound {
		t.Fatalf("signup with signup switched off: %d", w.Code)
	}
	closed := newSignupFixture(t, nil)
	if w := closed.call("GET", "/offer", "", closed.service.CustomerSignupOffer); w.Code != http.StatusNotFound {
		t.Fatalf("offer without a policy: %d", w.Code)
	}
}

// adr: 20261022100735 — a signed-in user creates their workspace on an
// offered plan: Gregale creates the Auth organization with the user's own
// session, the provider customer under a request-derived idempotency key,
// and runs onboarding. A retry returns the same workspace; another plan
// for the same request conflicts.
func TestProductSignupCreatesWorkspace(t *testing.T) {
	ctx := t.Context()
	f := newSignupFixture(t, &api.ProductSignupPolicy{Plans: []string{"starter", "pro"}, Provider: "stripe"})
	id := uuid.NewString()
	for _, bad := range []string{
		`{"id":"not-a-uuid","plan":"starter","workspace_name":"Acme"}`,
		`{"id":"` + id + `","plan":"enterprise","workspace_name":"Acme"}`,
		`{"id":"` + id + `","plan":"starter","workspace_name":"  "}`,
		`{"id":"` + strings.ToUpper(id) + `","plan":"starter","workspace_name":"Acme"}`,
	} {
		if w := f.call("POST", "/signup", bad, f.service.CustomerSignup); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid signup %s: %d %s", bad, w.Code, w.Body)
		}
	}
	cookies := f.cookies
	f.cookies = nil
	f.signup(id, "starter", "Acme", http.StatusUnauthorized)
	f.cookies = cookies
	if f.auth.creates != 0 || len(f.keys) != 0 {
		t.Fatalf("a refused signup reached Auth or the provider: %d %v", f.auth.creates, f.keys)
	}

	receipt := f.signup(id, "starter", "  Acme  ", http.StatusOK)
	slug := productcontrol.SignupOrganizationSlug(f.app.ID, id)
	if f.auth.creates != 1 || f.auth.orgs[0].Slug != slug || f.auth.orgs[0].Name != "Acme" || receipt.OrganizationID != f.auth.orgs[0].ID || receipt.Workspace != "Acme" {
		t.Fatalf("Auth organization: %+v receipt %+v", f.auth.orgs, receipt)
	}
	if len(f.keys) != 1 || f.keys[0] != "gregale-signup-"+f.app.ID+"-"+id {
		t.Fatalf("provider idempotency keys = %v", f.keys)
	}
	tenant, err := f.store.ResolvePlatformTenantExternalRef(ctx, f.account.ID, "cus_signup1")
	if err != nil || tenant.ID != receipt.Customer.TenantID {
		t.Fatalf("tenant = %+v %v", tenant, err)
	}
	customer, err := f.store.GetProductCustomer(ctx, f.account.ID, f.app.ID, tenant.ID)
	if err != nil || customer.Plan != "starter" || customer.AuthOrganizationID != receipt.OrganizationID {
		t.Fatalf("product customer = %+v %v", customer, err)
	}
	if len(f.host.audits) != 1 || f.host.audits[0]["kind"] != "product.customer.signed_up" {
		t.Fatalf("audits = %v", f.host.audits)
	}
	if data := f.host.audits[0]["data"].(map[string]any); data["tenant_id"] != tenant.ID || data["plan"] != "starter" || data["provider"] != "stripe" {
		t.Fatalf("audit data = %v", data)
	}

	again := f.signup(id, "starter", "Acme", http.StatusOK)
	if again.OrganizationID != receipt.OrganizationID || again.Customer.TenantID != receipt.Customer.TenantID || f.auth.creates != 1 || len(f.keys) != 1 {
		t.Fatalf("retry made another workspace: %+v creates %d keys %v", again, f.auth.creates, f.keys)
	}
	f.service.SignupCustomer = nil
	f.signup(id, "pro", "Acme", http.StatusConflict)
}

// adr: 20261022100735 — a provider failure leaves the organization for the
// retry to reuse, a provider customer another workspace bills is never
// taken over, and a user owns at most ProductControlSignupMaxWorkspacesPerUser
// workspaces.
func TestProductSignupRecoveryAndLimits(t *testing.T) {
	f := newSignupFixture(t, &api.ProductSignupPolicy{Plans: []string{"starter", "pro"}, Provider: "paddle"})
	unavailable := api.NewProblem(http.StatusBadGateway, api.CodeProductSignupUnavailable, "Signup unavailable", "Paddle is unavailable")
	f.provider = func(string) (string, *api.Problem) { return "", unavailable }
	id := uuid.NewString()
	f.signup(id, "pro", "First", http.StatusBadGateway)
	if f.auth.creates != 1 {
		t.Fatalf("organization creations = %d", f.auth.creates)
	}
	f.provider = func(string) (string, *api.Problem) { return "ctm_first", nil }
	first := f.signup(id, "pro", "First", http.StatusOK)
	if f.auth.creates != 1 || first.OrganizationID != f.auth.orgs[0].ID {
		t.Fatalf("retry did not reuse its organization: %d %+v", f.auth.creates, first)
	}

	// Paddle returns the same customer for the same email.
	before := len(f.auth.orgs)
	w := f.call("POST", "/signup", `{"id":"`+uuid.NewString()+`","plan":"starter","workspace_name":"Second"}`, f.service.CustomerSignup)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), api.CodeProductSignupLimit) {
		t.Fatalf("taken-over provider customer: %d %s", w.Code, w.Body)
	}
	customer, err := f.store.ProductCustomerByAuthOrganization(t.Context(), f.account.ID, f.app.ID, f.auth.orgs[before].ID)
	if err == nil {
		t.Fatalf("second organization was linked: %+v", customer)
	}

	n := 0
	f.provider = func(string) (string, *api.Problem) { n++; return "ctm_more" + string(rune('a'+n)), nil }
	for i := 1; i < api.ProductControlSignupMaxWorkspacesPerUser; i++ {
		f.signup(uuid.NewString(), "starter", "More", http.StatusOK)
	}
	creates := f.auth.creates
	w = f.call("POST", "/signup", `{"id":"`+uuid.NewString()+`","plan":"starter","workspace_name":"One too many"}`, f.service.CustomerSignup)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), api.CodeProductSignupLimit) || f.auth.creates != creates {
		t.Fatalf("workspace limit: %d %s creates %d", w.Code, w.Body, f.auth.creates-creates)
	}
}
