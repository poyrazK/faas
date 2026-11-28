package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261127191645 — a listing published with a tool contract gates the
// app: the owner reads the baseline, records audited overrides up to the
// open cap, and a live breaking candidate shows on the public changelog.
func TestMCPContract(t *testing.T) {
	ctx := context.Background()
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := httptest.NewServer(&consumerSignupStripe{})
	defer stripe.Close()
	client := &stripeinvoice.Client{BaseURL: stripe.URL, HTTP: stripe.Client()}
	(*e.S()).StatementInvoices().Stripe = client
	(*e.S()).ConsumerSignup().Stripe = client
	appID := apidserver.MustSeedApp(t, e, "search")
	store := *e.Store()
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

	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search/mcp-contract", nil, nil), http.StatusNotFound, nil)
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "x"}, nil),
		http.StatusNotFound, nil)

	contract, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{
		{Name: "search", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcphosting.MarshalContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	var listing api.MCPListingResponse
	decode(t, e.Do(t, http.MethodPut, "/v1/apps/search/mcp-listing", api.PutMCPListingRequest{Title: "Search", Endpoint: "https://" + u.Host + "/mcp",
		Tools: []api.MCPListingTool{{Name: "search"}}, Contract: raw, ProbeSecret: "MCP_PROBE_TOKEN"}, nil), http.StatusOK, &listing)
	if len(listing.ContractSHA256) != 64 || listing.ProbeSecret != "MCP_PROBE_TOKEN" {
		t.Fatalf("listing = %+v", listing)
	}

	var got api.MCPContractResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search/mcp-contract", nil, nil), http.StatusOK, &got)
	if got.BaselineSHA256 != listing.ContractSHA256 || !slices.Equal(got.Tools, []string{"search"}) || got.BaselineDeploymentID != "" ||
		len(got.Checks) != 0 || len(got.Overrides) != 0 {
		t.Fatalf("contract = %+v", got)
	}

	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: " "}, nil),
		http.StatusUnprocessableEntity, nil)
	var override api.MCPContractOverride
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "rename search",
		ExpiresInSeconds: 600}, nil), http.StatusCreated, &override)
	if override.Reason != "rename search" || override.CreatedBy == "" || override.ExpiresAt.Sub(override.CreatedAt).Minutes() < 9 {
		t.Fatalf("override = %+v", override)
	}
	for i := 1; i < api.MCPContractOverrideOpenCap; i++ {
		decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "more"}, nil),
			http.StatusCreated, nil)
	}
	var capped api.Problem
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "one too many"}, nil),
		http.StatusConflict, &capped)
	if capped.Limit == nil || *capped.Limit != api.MCPContractOverrideOpenCap {
		t.Fatalf("cap problem = %+v", capped)
	}

	// imaged records a breaking candidate, which consumes an override, and
	// the candidate goes live.
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:breaking",
		Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordMCPContractCheck(ctx, state.MCPContractCheck{DeploymentID: dep.ID, AppID: appID, AccountID: e.Acct().ID,
		BaselineSHA256: listing.ContractSHA256, CandidateSHA256: listing.ContractSHA256, Candidate: raw, Verdict: state.MCPContractBreaking,
		Changes: json.RawMessage(`[{"tool":"search","path":"/tools/search","kind":"removed","severity":"breaking"}]`)}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	decode(t, e.Do(t, http.MethodGet, "/v1/apps/search/mcp-contract", nil, nil), http.StatusOK, &got)
	if got.BaselineDeploymentID != dep.ID || len(got.Checks) != 1 || !got.Checks[0].Overridden || got.Checks[0].AcceptedAt == nil ||
		len(got.Overrides) != api.MCPContractOverrideOpenCap-1 {
		t.Fatalf("contract after live = %+v", got)
	}

	var public api.PublicMCPListingResponse
	decode(t, e.Do(t, http.MethodGet, "/v1/consumer-signup/search/listing", nil, map[string]string{"Authorization": ""}), http.StatusOK, &public)
	if len(public.Changelog) != 1 || public.Changelog[0].Verdict != state.MCPContractBreaking || !slices.Equal(public.Changelog[0].Tools, []string{"search"}) {
		t.Fatalf("public changelog = %+v", public.Changelog)
	}

	// An override can be bound to one of the app's own deployments.
	bound, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:bound",
		Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "bound",
		DeploymentID: "not-a-uuid"}, nil), http.StatusUnprocessableEntity, nil)
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "bound",
		DeploymentID: uuid.NewString()}, nil), http.StatusNotFound, nil)
	var boundOverride api.MCPContractOverride
	decode(t, e.Do(t, http.MethodPost, "/v1/apps/search/mcp-contract/overrides", api.CreateMCPContractOverrideRequest{Reason: "bound",
		DeploymentID: bound.ID}, nil), http.StatusCreated, &boundOverride)
	if boundOverride.DeploymentID != bound.ID {
		t.Fatalf("bound override = %+v", boundOverride)
	}
}
