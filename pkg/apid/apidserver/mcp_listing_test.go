package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
)

// adr: 20261126175800 — a seller publishes a listing for an app that takes
// sign-ups; the public JSON, page and server.json serve it until the
// seller unpublishes; the endpoint must stay on the app's host.
func TestMCPListing(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &consumerSignupStripe{}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	client := &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	apidserver.MustSeedApp(t, e, "search")
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
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: "pro", MaxUnitsPerMonth: 1000}, nil),
		http.StatusCreated, &plan)
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/rate-cards", api.CreateAPIConsumerRateCardRequest{Currency: "USD",
		PriceMillicentsPerUnit: 50, PlanID: plan.ID}, nil), http.StatusCreated, nil)
	decode(t, e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil),
		http.StatusOK, nil)
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/consumer-signup", api.PutConsumerSignupRequest{DisplayName: "Search",
		PlanIDs: []string{plan.ID}}, nil), http.StatusOK, nil)
	var app api.AppResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search", nil, nil), http.StatusOK, &app)
	u, err := url.Parse(app.URL)
	if err != nil || u.Host == "" {
		t.Fatalf("app url %q: %v", app.URL, err)
	}
	endpoint := "https://" + u.Host + "/mcp"
	public := map[string]string{"Authorization": ""}

	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search/mcp-listing", nil, nil), http.StatusNotFound, nil)
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search/listing", nil, public), http.StatusNotFound, nil)
	req := api.PutMCPListingRequest{Title: "Search", Summary: "Search the web from your agent.", Endpoint: "https://evil.example/mcp",
		Tools: []api.MCPListingTool{{Name: "search", Description: "Search the web"}}, RegistryName: "io.github.acme/search", ShowUsage: true}
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/mcp-listing", req, nil), http.StatusUnprocessableEntity, nil)
	req.Endpoint = endpoint
	var owner api.MCPListingResponse
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/mcp-listing", req, nil), http.StatusOK, &owner)
	if owner.Version != 1 || !strings.HasSuffix(owner.PageURL, "/v1/consumer-signup/search/listing/page") || owner.ServerJSONURL == "" {
		t.Fatalf("owner listing = %+v", owner)
	}

	var listing api.PublicMCPListingResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search/listing", nil, public), http.StatusOK, &listing)
	if listing.Title != "Search" || listing.Endpoint != endpoint || len(listing.Tools) != 1 || len(listing.Plans) != 1 ||
		!listing.SignupOpen || !strings.HasSuffix(listing.SignupURL, "/v1/consumer-signup/search/page") ||
		!strings.HasPrefix(listing.Install.CursorURL, "https://cursor.com/") || listing.Customers == nil || *listing.Customers != 0 {
		t.Fatalf("public listing = %+v", listing)
	}
	page := e.Do(t, http.MethodGet, "/v1/consumer-signup/search/listing/page", nil, public)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "listing.js") ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("page: %d %s", page.Code, page.Header())
	}
	if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/search/listing.js", nil, public); res.Code != http.StatusOK {
		t.Fatalf("script: %d", res.Code)
	}
	var server struct {
		Name    string `json:"name"`
		Remotes []struct {
			URL string `json:"url"`
		} `json:"remotes"`
	}
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search/server.json", nil, public), http.StatusOK, &server)
	if server.Name != "io.github.acme/search" || len(server.Remotes) != 1 || server.Remotes[0].URL != endpoint {
		t.Fatalf("server.json = %+v", server)
	}

	decode(t, e.Do(t, http.MethodDelete, "/v1/apps/search/mcp-listing", nil, nil), http.StatusNoContent, nil)
	for _, path := range []string{"/listing", "/listing/page", "/server.json"} {
		if res := e.Do(t, http.MethodGet, "/v1/consumer-signup/search"+path, nil, public); res.Code != http.StatusNotFound {
			t.Errorf("%s after unpublish: %d", path, res.Code)
		}
	}
}
