package hosting

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// RecoverDomain requires an explicit, reviewed provider ID and invalidates any
// outstanding lease. It is operator-only, never an automatic hostname adoption.
func (s Service) RecoverDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	host, ok := s.domainHost(w, acct)
	if !ok {
		return
	}
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req api.RecoverHostingDomainRequest
	if httpkit.DecodeJSON(r, &req) != nil || uuid.Validate(req.ProviderID) != nil || uuid.Validate(r.PathValue("domain")) != nil || req.ExpectedRevision < 1 {
		api.WriteProblem(w, api.ErrValidation("provider_id, expected_hostname, and expected_revision are required"))
		return
	}
	rows, err := host.HostingDomainStore().ListHostingDomains(r.Context(), acct.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	for _, d := range rows {
		if d.ID != r.PathValue("domain") {
			continue
		}
		recovered, err := recoverDomain(r.Context(), host, d, req)
		if err != nil {
			domainFailure(w, err)
			return
		}
		s.Host.HostingAudit(r.Context(), acct.ID, "hosting_domain_recovered", map[string]any{"site_id": site.ID, "domain_id": d.ID, "provider_id": req.ProviderID})
		httpkit.WriteJSON(w, http.StatusAccepted, recovered)
		return
	}
	domainFailure(w, state.ErrConflict)
}

func recoverDomain(ctx context.Context, host DomainHost, d api.HostingDomain, req api.RecoverHostingDomainRequest) (api.HostingDomain, error) {
	if d.ProviderOriginID != host.HostingDomainProvider().ID() || d.Hostname != req.ExpectedHostname || d.Revision != req.ExpectedRevision || d.Details.ProviderID != "" || d.Details.VerifiedAt == nil || d.Details.CreateStartedAt == nil || time.Since(*d.Details.CreateStartedAt) < api.HostingDomainRecoveryDelay {
		return api.HostingDomain{}, state.ErrConflict
	}
	validator, available := host.HostingDomainProvider().(interface {
		ValidateRecovery(context.Context, api.HostingDomain, string) error
	})
	if !available {
		return api.HostingDomain{}, state.ErrConflict
	}
	if err := validator.ValidateRecovery(ctx, d, req.ProviderID); err != nil {
		return api.HostingDomain{}, state.ErrConflict
	}
	return host.HostingDomainStore().RecoverHostingDomain(ctx, d.AccountID, d.SiteID, d.ID, req, time.Now().UTC())
}
