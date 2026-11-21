package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/sales"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

// An organization's first paid purchase can carry a salesperson code. Credit
// belongs to the organization; the buying member's own account stays free.
func TestPGOrganizationCheckoutCarriesSalespersonCode(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanFree)
	ctx := t.Context()
	store := *e.Store()
	buyer := e.Acct()
	if err := store.MarkAccountEmailVerified(ctx, buyer.ID); err != nil {
		t.Fatal(err)
	}
	org, err := store.(state.SharedOrgCreationStore).CreateSharedOrg(ctx, buyer.ID, "org-sales", "Acme Sales")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := store.CreateAccount(ctx, "rep-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, rep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(ctx, "UPDATE accounts SET mfa_secret_encrypted='\\x00'::bytea,mfa_enrolled_at=now() WHERE id=$1", rep.ID); err != nil {
		t.Fatal(err)
	}
	refs := store.(interface{ ReferralStore() referrals.Store }).ReferralStore()
	link, err := refs.EnsureLink(ctx, rep.ID, "sales")
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithSales(sales.Config{Enabled: true, Members: map[string]bool{strings.ToLower(rep.Email): false},
		Policy: referrals.Policy{Currency: "usd", CommissionBPS: 1000, ConversionDays: 90}}, refs)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/customers/external/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": uuid.NewString()})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkouts":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "checkout-test", "url": "https://checkout.example/test"})
		default:
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	provider, err := polar.NewProvider(polar.Config{APIKey: "offline-test-key", WebhookSecret: orgSubjectWebhookSecret, BaseURL: endpoint.URL, ProProductID: "product-pro"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithBillingProvider(provider)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		e.AddAdminSession(t, req)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Active-Org", org.Slug)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rr := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rr, req)
		return rr
	}
	rr := request(http.MethodGet, "/v1/orgs/"+org.Slug+"/referrals/checkout", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"eligible":true`) {
		t.Fatalf("eligibility before purchase: %d %s", rr.Code, rr.Body.String())
	}
	rr = request(http.MethodPatch, "/v1/orgs/"+org.Slug+"/plan", `{"plan":"pro","salesperson_code":"`+strings.ToLower(link.Code)+`"}`)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("organization checkout with code: %d %s", rr.Code, rr.Body.String())
	}
	rr = request(http.MethodGet, "/v1/orgs/"+org.Slug+"/referrals/checkout", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "already_attributed") {
		t.Fatalf("eligibility after selection: %d %s", rr.Code, rr.Body.String())
	}
	rows, err := refs.List(ctx, referrals.Scope{OwnerID: rep.ID}, "", 25)
	if err != nil || len(rows) != 1 || rows[0].AccountID != org.ID || rows[0].SubjectKind != referrals.SubjectOrg ||
		rows[0].OrgName != "Acme Sales" || !strings.EqualFold(rows[0].Email, buyer.Email) || rows[0].CommissionBPS != 1000 {
		t.Fatalf("organization credit: %+v %v", rows, err)
	}
	status, err := refs.(referrals.CheckoutStore).CheckoutState(ctx, buyer.ID)
	if err != nil || !status.Eligible {
		t.Fatalf("buyer's own account consumed: %+v %v", status, err)
	}
}
