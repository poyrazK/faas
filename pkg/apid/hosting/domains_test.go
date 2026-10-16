package hosting

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
