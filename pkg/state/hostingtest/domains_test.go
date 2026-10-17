package hostingtest

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func domainInput(account state.Account, site api.HostingSite) api.HostingDomain {
	return api.HostingDomain{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, Hostname: "portal-" + uuid.NewString() + ".customer.example", ProviderOriginID: strings.Repeat("a", 64), Challenge: strings.Repeat("b", 64)}
}
func customDomainLifecycle(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	other, otherSite := seedDurable(t, store)
	d, err := store.CreateHostingDomain(t.Context(), domainInput(account, site))
	if err != nil {
		t.Fatal(err)
	}
	foreign := domainInput(other, otherSite)
	foreign.Hostname = d.Hostname
	if _, err = store.CreateHostingDomain(t.Context(), foreign); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate global claim: %v", err)
	}
	if _, err = store.ListHostingDomains(t.Context(), other.ID, site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account listing: %v", err)
	}
	now := time.Now().UTC().Add(time.Second)
	firstToken := uuid.NewString()
	leases, err := store.ClaimHostingDomains(t.Context(), now, firstToken)
	if err != nil || len(leases) != 1 {
		t.Fatalf("claim: %v %v", leases, err)
	}
	old := leases[0]
	if rows, err := store.ClaimHostingDomains(t.Context(), now, uuid.NewString()); err != nil || len(rows) != 0 {
		t.Fatalf("concurrent claim: %v %v", rows, err)
	}
	now = now.Add(api.HostingDomainLease + time.Second)
	token := uuid.NewString()
	leases, err = store.ClaimHostingDomains(t.Context(), now, token)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease recovery: %v", err)
	}
	lease := leases[0]
	details := api.HostingDomainDetails{TLSStatus: "active", ProviderID: uuid.NewString(), AccessID: uuid.NewString(), AccessAUD: strings.Repeat("c", 64), VerifiedAt: &now, Records: []api.HostingDNSRecord{}}
	if _, err = store.SaveHostingDomain(t.Context(), old, "active", details); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired owner saved: %v", err)
	}
	lease, err = store.SaveHostingDomain(t.Context(), lease, "active", details)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingDomain(t.Context(), lease, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListHostingDomains(t.Context(), account.ID, site.ID)
	if err != nil || len(rows) != 1 || rows[0].State != "deleting" {
		t.Fatalf("site deletion lost domain cleanup: %+v %v", rows, err)
	}
	if _, err = store.SaveHostingDomain(t.Context(), lease, "active", details); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late activation survived deletion: %v", err)
	}
	if err = store.FinishHostingDomain(t.Context(), lease, now, "provider_unavailable"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale retry postponed deletion: %v", err)
	}
	if err = store.ReleaseHostingDomainBatch(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	leases, err = store.ClaimHostingDomains(t.Context(), now, uuid.NewString())
	if err != nil || len(leases) != 1 {
		t.Fatalf("deletion claim: %v", err)
	}
	lease = leases[0]
	if err = store.PurgeHostingDomain(t.Context(), lease, now.Add(time.Hour)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("purged without deletion receipt: %v", err)
	}
	if err = store.AcknowledgeHostingDomain(t.Context(), lease, now); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingDomain(t.Context(), lease, now.Add(api.HostingDomainRemovalGrace-time.Second)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("purged within cache grace: %v", err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, site.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("site purge lost domain catalog: %v", err)
	}
	if err = store.PurgeHostingDomain(t.Context(), lease, now.Add(api.HostingDomainRemovalGrace+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateHostingDomain(t.Context(), foreign); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("hostname reservation lost after purge: %v", err)
	}
}

func customDomainQuota(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	var group sync.WaitGroup
	results := make(chan error, api.HostingDomainsPerSite+1)
	for range api.HostingDomainsPerSite + 1 {
		group.Go(func() { _, err := store.CreateHostingDomain(t.Context(), domainInput(account, site)); results <- err })
	}
	group.Wait()
	close(results)
	created, limited := 0, 0
	for err := range results {
		if err == nil {
			created++
		} else if errors.Is(err, state.ErrHostingQuota) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if created != api.HostingDomainsPerSite || limited != 1 {
		t.Fatalf("concurrent quota: %d created %d limited", created, limited)
	}
}

func customDomainRecovery(t *testing.T, store hostingTestStore) {
	account, site := seedDurable(t, store)
	d, err := store.CreateHostingDomain(t.Context(), domainInput(account, site))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	leases, err := store.ClaimHostingDomains(t.Context(), now, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var lease state.HostingDomainLease
	for _, item := range leases {
		if item.Domain.ID == d.ID {
			lease = item
		}
	}
	started := now
	details := lease.Domain.Details
	details.VerifiedAt, details.CreateStartedAt = &started, &started
	lease, err = store.SaveHostingDomain(t.Context(), lease, "pending_certificate", details)
	if err != nil {
		t.Fatal(err)
	}
	req := api.RecoverHostingDomainRequest{ExpectedRevision: d.Revision, ExpectedHostname: d.Hostname, ProviderID: uuid.NewString()}
	if _, err := store.RecoverHostingDomain(t.Context(), account.ID, site.ID, d.ID, req, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("recovery before in-flight window closed: %v", err)
	}
	later := now.Add(api.HostingDomainRecoveryDelay + time.Second)
	wrong := req
	wrong.ExpectedHostname = "other.customer.example"
	if _, err := store.RecoverHostingDomain(t.Context(), account.ID, site.ID, d.ID, wrong, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong hostname: %v", err)
	}
	if _, err := store.RecoverHostingDomain(t.Context(), uuid.NewString(), site.ID, d.ID, req, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-account recovery: %v", err)
	}
	// Recovery must also unblock cleanup after deletion, without reactivating it.
	deleting, err := store.DeleteHostingDomain(t.Context(), account.ID, site.ID, d.ID, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverHostingDomain(t.Context(), account.ID, site.ID, d.ID, req, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale recovery: %v", err)
	}
	req.ExpectedRevision = deleting.Revision
	recovered, err := store.RecoverHostingDomain(t.Context(), account.ID, site.ID, d.ID, req, later)
	if err != nil || recovered.State != "deleting" || recovered.Details.ProviderID != req.ProviderID || recovered.Generation != deleting.Generation {
		t.Fatalf("recovery changed routing/deletion: %+v %v", recovered, err)
	}
	if _, err := store.SaveHostingDomain(t.Context(), lease, "pending_certificate", details); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker saved after recovery: %v", err)
	}
	if _, err := store.RecoverHostingDomain(t.Context(), account.ID, site.ID, d.ID, req, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("bound ID overwritten: %v", err)
	}
}
