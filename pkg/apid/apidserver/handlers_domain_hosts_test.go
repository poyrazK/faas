package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-520 amendment 2: GET, PUT and DELETE /v1/domains/{domain}/hosts manage
// a wildcard domain's host allowlist. Only the domain's owner reaches them,
// and only wildcard domains have one.
func TestDomainHostAllowlistRoutes(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	appID := apidserver.MustSeedApp(t, e, "allowlist-app")
	if _, err := store.CreateCustomDomain(ctx, "*.allow.example.com", appID, "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "exact.example.com", appID, "tok"); err != nil {
		t.Fatal(err)
	}
	decode := func(t *testing.T, body []byte) api.CustomDomainHostsResponse {
		t.Helper()
		var out api.CustomDomainHostsResponse
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return out
	}
	const path = "/v1/domains/*.allow.example.com/hosts"

	rec := e.Do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET without allowlist: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec.Body.Bytes()); got.Restricted || len(got.Names) != 0 || got.Domain != "*.allow.example.com" {
		t.Fatalf("GET without allowlist = %+v", got)
	}

	rec = e.Do(t, http.MethodPut, path, api.SetCustomDomainHostsRequest{Names: []string{"App", "eu.shop.allow.example.com", "api"}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec.Body.Bytes()); !got.Restricted || !reflect.DeepEqual(got.Names, []string{"api", "app", "eu.shop"}) {
		t.Fatalf("PUT = %+v", got)
	}
	if ok, err := store.CustomDomainTLSHostAllowed(ctx, "*.allow.example.com", "random.allow.example.com"); err != nil || ok {
		t.Fatalf("an unlisted host after PUT = %v, %v; want refused", ok, err)
	}

	for name, req := range map[string]api.SetCustomDomainHostsRequest{
		"empty":   {},
		"invalid": {Names: []string{"bad_name"}},
	} {
		rec := e.Do(t, http.MethodPut, path, req, nil)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("PUT %s: status=%d body=%s; want 400 or 422", name, rec.Code, rec.Body.String())
		}
	}
	many := make([]string, api.CustomDomainTLSAllowedHostsMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("h%d", i)
	}
	rec = e.Do(t, http.MethodPut, path, api.SetCustomDomainHostsRequest{Names: many}, nil)
	body := rec.Body.Bytes()
	apidserver.AssertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeValidation)
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil || problem.Limit == nil || *problem.Limit != int64(api.CustomDomainTLSAllowedHostsMax) {
		t.Fatalf("too many names: problem %s (%v), want limit %d", body, err, api.CustomDomainTLSAllowedHostsMax)
	}
	rec = e.Do(t, http.MethodPut, "/v1/domains/exact.example.com/hosts", api.SetCustomDomainHostsRequest{Names: []string{"app"}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeValidation)

	rec = e.Do(t, http.MethodDelete, path, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if names, err := store.CustomDomainTLSAllowedHosts(ctx, "*.allow.example.com"); err != nil || len(names) != 0 {
		t.Fatalf("allowlist after DELETE = %v, %v", names, err)
	}

	// Another account's wildcard is not found, as for the domain's other routes.
	other, err := store.CreateAccount(ctx, "allowlist-other@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "allowlist-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.other.example.com", otherApp.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := e.Do(t, method, "/v1/domains/*.other.example.com/hosts", api.SetCustomDomainHostsRequest{Names: []string{"app"}}, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s another account's allowlist: status=%d body=%s; want 404", method, rec.Code, rec.Body.String())
		}
	}
	if names, err := store.CustomDomainTLSAllowedHosts(ctx, "*.other.example.com"); err != nil || len(names) != 0 {
		t.Fatalf("another account's allowlist changed: %v, %v", names, err)
	}
}
