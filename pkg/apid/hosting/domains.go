package hosting

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type DomainHost interface {
	HostingDomainsEnabled() bool
	HostingDomainStore() state.HostingDomainStore
	HostingDomainProvider() artifacts.DomainProvider
	HostingReservedDomains() []string
}

func (s Service) RequireDomains(next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if _, ok := s.domainHost(w, acct); ok {
			next(w, r, acct)
		}
	}
}

func domainFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(409, "hosting_domain_conflict", "Domain state changed", "refresh the domain; hostname reservations, current revisions, and an active durable site are required"))
		return
	}
	if errors.Is(err, state.ErrHostingQuota) {
		p := api.NewProblem(409, "hosting_domain_quota", "Domain limit reached", "remove an unused domain and wait for safe cleanup before adding another")
		var quota *state.HostingQuotaError
		if errors.As(err, &quota) {
			p.Limit, p.Observed = &quota.Limit, &quota.Observed
		}
		p.DocsURL = "https://github.com/poyrazK/faas/blob/main/docs/internal-frontend-hosting-domains.md"
		api.WriteProblem(w, p)
		return
	}
	failure(w, err)
}

func (s Service) domainHost(w http.ResponseWriter, acct state.Account) (DomainHost, bool) {
	if !s.guard(w, acct) {
		return nil, false
	}
	host, ok := s.Host.(DomainHost)
	if !ok || !host.HostingDomainsEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_domains_disabled", "Custom domains unavailable", "internal hosting custom domains are disabled"))
		return nil, false
	}
	if host.HostingDomainProvider() == nil || host.HostingDomainStore() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_domains_unconfigured", "Custom domains unavailable", "custom-domain providers are not configured"))
		return nil, false
	}
	return host, true
}

func (s Service) ListDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	host, ok := s.domainHost(w, acct)
	if !ok {
		return
	}
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	rows, err := host.HostingDomainStore().ListHostingDomains(r.Context(), acct.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, rows)
}

func (s Service) CreateDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	host, ok := s.domainHost(w, acct)
	if !ok {
		return
	}
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateHostingDomainRequest
	if httpkit.DecodeJSON(r, &req) != nil {
		api.WriteProblem(w, api.ErrValidation("hostname is required"))
		return
	}
	name, err := artifacts.NormalizeDomain(req.Hostname, host.HostingReservedDomains())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("use a public CNAME subdomain outside platform and hosting domains"))
		return
	}
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		failure(w, err)
		return
	}
	d, err := host.HostingDomainStore().CreateHostingDomain(r.Context(), api.HostingDomain{ID: uuid.NewString(), AccountID: acct.ID, SiteID: site.ID, Hostname: name, ProviderOriginID: host.HostingDomainProvider().ID(), Challenge: hex.EncodeToString(challenge)})
	if err != nil {
		domainFailure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_domain_created", map[string]any{"site_id": site.ID, "domain_id": d.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, d)
}

func (s Service) DeleteDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	host, ok := s.domainHost(w, acct)
	if !ok {
		return
	}
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req api.DeleteHostingDomainRequest
	if uuid.Validate(r.PathValue("domain")) != nil || httpkit.DecodeJSON(r, &req) != nil || req.ExpectedRevision < 1 {
		api.WriteProblem(w, api.ErrValidation("domain and expected_revision are required"))
		return
	}
	d, err := host.HostingDomainStore().DeleteHostingDomain(r.Context(), acct.ID, site.ID, r.PathValue("domain"), req.ExpectedRevision)
	if err != nil {
		domainFailure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_domain_deletion_requested", map[string]any{"site_id": site.ID, "domain_id": d.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, d)
}
