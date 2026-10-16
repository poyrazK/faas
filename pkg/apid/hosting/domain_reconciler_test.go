package hosting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type domainProviderFixture struct {
	ready, removeFail bool
	uncertain         bool
	creates           int
	calls, removals   int
	after             func()
	err               error
}

func (*domainProviderFixture) ID() string     { return strings.Repeat("b", 64) }
func (*domainProviderFixture) Target() string { return "customers.hosting.example" }
func (p *domainProviderFixture) Ensure(_ context.Context, d api.HostingDomain, allowCreate bool) (api.HostingDomainDetails, bool, error) {
	p.calls++
	if d.Details.CreateStartedAt == nil {
		panic("provider called without durable create fence")
	}
	if d.Details.ProviderID == "" {
		if !allowCreate {
			return d.Details, false, artifacts.ErrDomainProviderConflict
		}
		p.creates++
		if p.uncertain {
			return d.Details, false, artifacts.ErrDomainProviderUnavailable
		}
	}
	if p.after != nil {
		p.after()
	}
	details := d.Details
	details.ProviderID = "11111111-1111-4111-8111-111111111111"
	details.TLSStatus = "pending"
	details.Records = []api.HostingDNSRecord{{Type: "TXT", Name: "_acme-challenge." + d.Hostname, Value: "certificate-proof"}}
	if p.ready {
		details.TLSStatus = "active"
		details.AccessID = "22222222-2222-4222-8222-222222222222"
		details.AccessAUD = strings.Repeat("c", 64)
	}
	return details, p.ready, p.err
}
func (p *domainProviderFixture) Remove(context.Context, api.HostingDomain) error {
	p.removals++
	if p.removeFail {
		return errors.New("provider unavailable")
	}
	return nil
}

type domainDNSFixture struct {
	valid             bool
	challenge, target string
}

func (d *domainDNSFixture) LookupTXT(context.Context, string) ([]string, error) {
	if !d.valid {
		return nil, nil
	}
	return []string{"gregale-hosting=" + d.challenge}, nil
}
func (d *domainDNSFixture) LookupCNAME(context.Context, string) (string, error) {
	return d.target + ".", nil
}

type domainPublisherFixture struct {
	head    artifacts.DomainRoute
	failure bool
	writes  int
}

func (p *domainPublisherFixture) PutDomain(_ context.Context, r artifacts.DomainRoute) error {
	if p.failure {
		return errors.New("storage unavailable")
	}
	if p.head != r {
		p.writes++
	}
	p.head = r
	return nil
}

func domainReconcilerFixture(t *testing.T) (*state.MemStore, api.HostingSite, api.HostingDomain, *domainProviderFixture, *domainDNSFixture, *domainPublisherFixture, *time.Time, DomainReconciler) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "hosting"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	site, err := store.CreateHostingSite(t.Context(), api.HostingSite{ID: id, AccountID: account.ID, ProjectID: project.ID, Environment: "production", Name: "web", Hostname: id + ".hosting.example", OriginID: strings.Repeat("a", 64), DurableRouting: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := &domainProviderFixture{}
	domain, err := store.CreateHostingDomain(t.Context(), api.HostingDomain{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, Hostname: "portal.customer.example", ProviderOriginID: provider.ID(), Challenge: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	dns := &domainDNSFixture{challenge: domain.Challenge, target: provider.Target()}
	publisher := &domainPublisherFixture{}
	now := time.Now().UTC().Add(time.Second)
	r := DomainReconciler{Store: store, Sites: store, Provider: provider, Publisher: publisher, OriginID: site.OriginID, DNS: dns, Now: func() time.Time { return now }}
	return store, site, domain, provider, dns, publisher, &now, r
}

func TestDomainReconcilerVerifiesPublishesAndRemovesAfterGrace(t *testing.T) {
	store, site, domain, provider, dns, publisher, now, r := domainReconcilerFixture(t)
	read := func() api.HostingDomain {
		rows, err := store.ListHostingDomains(t.Context(), site.AccountID, site.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("domain rows: %v %v", rows, err)
		}
		return rows[0]
	}
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 || read().LastErrorCode != "dns_pending" {
		t.Fatal("provider reached before ownership proof")
	}
	dns.valid = true
	*now = now.Add(2 * api.HostingDomainRetryInterval)
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if read().State != "pending_certificate" || publisher.head.Enabled || read().Details.VerifiedAt == nil {
		t.Fatal("pending certificate served")
	}
	provider.ready = true
	publisher.failure = true
	*now = now.Add(2 * api.HostingDomainRetryInterval)
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("missing publication error")
	}
	if read().LastErrorCode != "publication_unavailable" || read().PublishedGeneration == read().Generation {
		t.Fatal("publication failure reported as complete")
	}
	publisher.failure = false
	*now = now.Add(2 * api.HostingDomainRetryInterval)
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !publisher.head.Enabled || read().PublishedGeneration != read().Generation {
		t.Fatal("verified domain not published")
	}
	current := read()
	if _, err := store.DeleteHostingDomain(t.Context(), site.AccountID, site.ID, domain.ID, current.Revision); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !publisher.head.Deleted || publisher.head.Enabled || provider.removals != 0 {
		t.Fatal("provider removal preceded terminal publication and grace")
	}
	provider.removeFail = true
	*now = now.Add(api.HostingDomainRemovalGrace + api.HostingDomainRetryInterval)
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("lost cleanup error")
	}
	if read().State != "deleting" {
		t.Fatal("lost cleanup catalog")
	}
	provider.removeFail = false
	*now = now.Add(2 * api.HostingDomainRetryInterval)
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListHostingDomains(t.Context(), site.AccountID, site.ID)
	if err != nil || len(rows) != 0 || !publisher.head.Deleted {
		t.Fatalf("cleanup: %v %v", rows, err)
	}
}

func TestDomainReconcilerLateProviderResultCannotReactivateDeletion(t *testing.T) {
	store, site, domain, provider, dns, publisher, _, r := domainReconcilerFixture(t)
	dns.valid = true
	provider.ready = true
	provider.after = func() {
		_, err := store.DeleteHostingDomain(t.Context(), site.AccountID, site.ID, domain.ID, domain.Revision)
		if err != nil {
			t.Error(err)
		}
	}
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("stale result was acknowledged")
	}
	if publisher.head.Enabled {
		t.Fatal("late provider result published an enabled alias")
	}
	provider.after = nil
	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !publisher.head.Deleted {
		t.Fatal("deletion was postponed by stale completion")
	}
}

func TestDomainReconcilerUncertainCreateNeverRepeats(t *testing.T) {
	store, site, _, provider, dns, _, now, r := domainReconcilerFixture(t)
	dns.valid, provider.uncertain = true, true
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("uncertain create reported success")
	}
	rows, _ := store.ListHostingDomains(t.Context(), site.AccountID, site.ID)
	if rows[0].Details.CreateStartedAt == nil || rows[0].Details.ProviderID != "" {
		t.Fatal("create fence lost")
	}
	*now = now.Add(api.HostingDomainRetryInterval * 2)
	if err := r.RunOnce(t.Context()); err == nil {
		t.Fatal("unresolved create retried")
	}
	if provider.creates != 1 {
		t.Fatalf("create attempts: %d", provider.creates)
	}
	rows, _ = store.ListHostingDomains(t.Context(), site.AccountID, site.ID)
	if rows[0].LastErrorCode != "provider_conflict" {
		t.Fatal("recovery not surfaced")
	}
}

func (*domainProviderFixture) ValidateRecovery(_ context.Context, d api.HostingDomain, id string) error {
	if d.Hostname != "portal.customer.example" || id != "11111111-1111-4111-8111-111111111111" {
		return artifacts.ErrDomainProviderConflict
	}
	return nil
}
