package hosting

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainAPIHasSeparateFlagAndOwnershipBoundary(t *testing.T) {
	store, site, _, provider, _, _, _, _ := domainReconcilerFixture(t)
	host := &testHost{enabled: true, allowed: true, store: store, artifacts: &testArtifacts{}, domainProvider: provider}
	service := Service{Host: host}
	request := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		if method == "GET" {
			service.ListDomains(w, r, state.Account{ID: site.AccountID})
		} else {
			service.CreateDomain(w, r, state.Account{ID: site.AccountID})
		}
		return w
	}
	if w := request("GET", ""); w.Code != 503 || !strings.Contains(w.Body.String(), "frontend_hosting_domains_disabled") {
		t.Fatalf("default gate: %d %s", w.Code, w.Body)
	}
	host.domains = true
	for _, name := range []string{"evil.gregale.dev", "portal.hosting.example", "example.com", "*.customer.example"} {
		w := request("POST", `{"hostname":"`+name+`"}`)
		if w.Code != 422 && w.Code != 400 {
			t.Fatalf("reserved domain %q: %d %s", name, w.Code, w.Body)
		}
	}
	w := request("POST", `{"hostname":"New.Customer.Example"}`)
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var domain api.HostingDomain
	if err := json.Unmarshal(w.Body.Bytes(), &domain); err != nil || domain.Hostname != "new.customer.example" || domain.State != "pending_dns" {
		t.Fatalf("new claim: %+v %v", domain, err)
	}
	host.domains = false
	replayed := httptest.NewRecorder()
	service.RequireDomains(func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(200) })(replayed, httptest.NewRequest("POST", "/", nil), state.Account{ID: site.AccountID})
	if replayed.Code != 503 {
		t.Fatal("disabled domain flag did not wrap replay")
	}
}

func TestDomainRecoveryRequiresReviewedIdentityAndRevision(t *testing.T) {
	store, site, domain, provider, _, _, _, _ := domainReconcilerFixture(t)
	now := time.Now().UTC()
	leases, err := store.ClaimHostingDomains(t.Context(), now.Add(time.Second), uuid.NewString())
	if err != nil || len(leases) != 1 {
		t.Fatalf("claim: %v", err)
	}
	details := leases[0].Domain.Details
	past := now.Add(-api.HostingDomainRecoveryDelay - time.Second)
	details.VerifiedAt, details.CreateStartedAt = &past, &past
	if _, err := store.SaveHostingDomain(t.Context(), leases[0], "pending_certificate", details); err != nil {
		t.Fatal(err)
	}
	host := &testHost{enabled: true, allowed: true, domains: true, store: store, artifacts: &testArtifacts{}, domainProvider: provider}
	service := Service{Host: host}
	request := func(revision int64, hostname, id string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.RecoverHostingDomainRequest{ExpectedRevision: revision, ExpectedHostname: hostname, ProviderID: id})
		r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		r.SetPathValue("site", site.ID)
		r.SetPathValue("domain", domain.ID)
		w := httptest.NewRecorder()
		service.RecoverDomain(w, r, state.Account{ID: site.AccountID})
		return w
	}
	id := "11111111-1111-4111-8111-111111111111"
	if w := request(9, domain.Hostname, id); w.Code != 409 {
		t.Fatalf("stale revision: %d", w.Code)
	}
	if w := request(1, "wrong.customer.example", id); w.Code != 409 {
		t.Fatalf("wrong hostname: %d", w.Code)
	}
	if w := request(1, domain.Hostname, uuid.NewString()); w.Code != 409 {
		t.Fatalf("foreign provider: %d", w.Code)
	}
	if w := request(1, domain.Hostname, id); w.Code != 202 {
		t.Fatalf("recovery: %d %s", w.Code, w.Body)
	}
	if w := request(1, domain.Hostname, id); w.Code != 409 {
		t.Fatalf("stale rebind: %d", w.Code)
	}
}
