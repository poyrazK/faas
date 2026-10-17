// ADR-1337: verified workspace onboarding, retained setup and activation evidence.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func TestProductOnboardingReviewAndConfirmation(t *testing.T) {
	org, authID, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	catalog, err := productcontrol.Compile(api.ProductCatalogSpec{AuthApp: "auth", Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	previews, confirms := 0, 0
	var saved api.ProductOnboardingPreview
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer owner" {
			t.Error("owner API credential missing")
		}
		switch r.URL.Path {
		case "/v1/apps/exports/product/onboarding/workspaces":
			var body api.ProductOnboardingAuthRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.AuthAccessToken != "short.access.token" {
				t.Error("missing transient user token")
			}
			_ = json.NewEncoder(w).Encode(api.ProductOnboardingWorkspaces{AuthApp: "auth", AuthAppID: authID, Workspaces: []api.ProductOnboardingWorkspace{{ID: org, Slug: "acme", Name: "Acme", Role: "owner"}}})
		case "/v1/apps/exports/product/onboarding/preview":
			previews++
			var body api.ProductOnboardingReviewRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Request.OrganizationID != org || body.Request.Plan != "starter" || body.AuthAccessToken != "short.access.token" {
				t.Error("preview changed requested workspace or plan")
			}
			body.Request.Revision = catalog.Revision
			saved = api.ProductOnboardingPreview{Request: body.Request, Catalog: catalog, Hash: strings.Repeat("a", 64), CreatesTenant: true, Tenant: api.ProductOnboardingTenant{ExternalRef: "acme", Name: "Acme customer", Status: "active"}}
			_ = json.NewEncoder(w).Encode(saved)
		case "/v1/apps/exports/product/onboarding/confirm":
			confirms++
			var body api.ProductOnboardingApplyRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Confirmation.Request != saved.Request || body.Confirmation.ExpectedHash != saved.Hash {
				t.Error("confirmation changed saved request")
			}
			_ = json.NewEncoder(w).Encode(api.ProductOnboardingReceipt{ID: saved.Request.ID, Request: saved.Request, Tenant: api.ProductOnboardingTenant{ID: tenant, Name: "Acme customer"}, Customer: api.ProductCustomer{TenantID: tenant, Plan: "starter", Revision: catalog.Revision, AuthOrganizationID: org}})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	reviewFile := filepath.Join(dir, "onboarding.json")
	if err = os.WriteFile(tokenFile, []byte("short.access.token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(srv.URL, "owner")
	c, err := parseProductCommand([]string{"customers", "onboard", "--app", "exports", "--external-ref", "acme", "--name", "Acme customer", "--plan", "starter", "--workspace", "acme", "--auth-token-file", tokenFile, "--file", reviewFile})
	if err != nil {
		t.Fatal(err)
	}
	value, err := runProductCommand(t.Context(), client, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(reviewFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "short.access.token") || strings.Contains(string(raw), "auth_access_token") {
		t.Fatal("review retained a credential")
	}
	info, err := os.Stat(reviewFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("review permissions: %v %v", info, err)
	}
	mu.Lock()
	if previews != 1 || confirms != 0 {
		t.Errorf("review mutated customer: previews=%d confirms=%d", previews, confirms)
	}
	mu.Unlock()
	var output bytes.Buffer
	if err = writeProductOutput(&output, value, false); err != nil || !strings.Contains(output.String(), "25000 EUR millicents") {
		t.Fatalf("price review: %s %v", output.String(), err)
	}
	if _, err = runProductCommand(t.Context(), client, c); err == nil {
		t.Fatal("existing review overwritten")
	}
	if again, _ := os.ReadFile(reviewFile); !bytes.Equal(raw, again) {
		t.Fatal("saved confirmation changed")
	}
	c, err = parseProductCommand([]string{"customers", "onboard", "--app", "exports", "--file", reviewFile, "--confirm", "--auth-token-file", tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = runProductCommand(t.Context(), client, c); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if previews != 1 || confirms != 2 {
		t.Errorf("confirmation did not preserve request: previews=%d confirms=%d", previews, confirms)
	}
	mu.Unlock()
	// Catalog bytes in a saved review must still hash to the reviewed revision.
	p, err := readProductOnboardingPreview(reviewFile)
	if err != nil {
		t.Fatal(err)
	}
	plan := p.Catalog.Spec.Plans["starter"]
	grant := plan.Features["export"]
	grant.UnitPriceMillicents = 1
	plan.Features["export"] = grant
	p.Catalog.Spec.Plans["starter"] = plan
	if validateProductOnboardingPreview(p) == nil {
		t.Fatal("tampered displayed price accepted")
	}
}

func TestProductOnboardingSelectorsAndArguments(t *testing.T) {
	list := api.ProductOnboardingWorkspaces{AuthApp: "auth", Workspaces: []api.ProductOnboardingWorkspace{{ID: uuid.NewString(), Slug: "first", Name: "Acme", Role: "owner"}, {ID: uuid.NewString(), Slug: "second", Name: "Acme", Role: "admin"}}}
	if _, err := selectProductOnboardingWorkspace(list, "Acme"); err == nil {
		t.Fatal("ambiguous workspace name accepted")
	}
	if _, err := selectProductOnboardingWorkspace(list, "first"); err != nil {
		t.Fatal(err)
	}
	list.Workspaces[0].Role = "member"
	if _, err := selectProductOnboardingWorkspace(list, "first"); err == nil {
		t.Fatal("ordinary member can link workspace")
	}
	for _, args := range [][]string{{"customers", "workspaces", "--app", "exports"}, {"customers", "onboard", "--app", "exports"}, {"customers", "onboard", "--app", "exports", "--confirm", "--plan", "pro"}, {"customers", "onboarding", "--app", "exports", "--id", "invalid"}} {
		if _, err := parseProductCommand(args); err == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "bad-token")
	if err := os.WriteFile(path, []byte("sensitive/token/value"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProductOnboardingToken(path); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("token error disclosed contents: %v", err)
	}
}
