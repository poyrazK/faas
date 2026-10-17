package hosting

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

type OIDCHost interface {
	HostingOIDCEnabled() bool
	HostingOIDCStore() state.HostingOIDCStore
	HostingOIDCAccount(context.Context, string) (state.Account, error)
}

func (s Service) oidcHost(w http.ResponseWriter) (OIDCHost, bool) {
	w.Header().Set("Cache-Control", "no-store")
	h, ok := s.Host.(OIDCHost)
	a, automation := s.Host.(AutomationHost)
	if !ok || !automation || !s.Host.HostingEnabled() || !s.Host.HostingDurableRoutingEnabled() || !a.HostingAutomationEnabled() || !h.HostingOIDCEnabled() || h.HostingOIDCStore() == nil || s.OIDCVerifier == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_oidc_disabled", "GitHub site identity unavailable", "internal hosting OIDC is disabled or unconfigured"))
		return nil, false
	}
	return h, true
}

func (s Service) ListOIDCTrusts(w http.ResponseWriter, r *http.Request, account state.Account) {
	if !s.guard(w, account) {
		return
	}
	h, ok := s.oidcHost(w)
	if !ok {
		return
	}
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	rows, err := h.HostingOIDCStore().ListHostingOIDCTrusts(r.Context(), account.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, rows)
}

func (s Service) CreateOIDCTrust(w http.ResponseWriter, r *http.Request, account state.Account) {
	if !s.guard(w, account) {
		return
	}
	h, ok := s.oidcHost(w)
	if !ok {
		return
	}
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	var rules api.HostingOIDCTrustRules
	if httpkit.DecodeJSONSized(r, &rules, api.HostingOIDCRequestMaxBytes) != nil || !artifacts.ValidHostingOIDCTrust(rules) {
		api.WriteProblem(w, api.ErrValidation("exact GitHub repository IDs, repository, branch ref, workflow ref and push/workflow_dispatch/schedule event are required"))
		return
	}
	trust, err := h.HostingOIDCStore().CreateHostingOIDCTrust(r.Context(), api.HostingOIDCTrust{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, HostingOIDCTrustRules: rules, CreatedAt: time.Now().UTC()})
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), account.ID, "hosting_oidc_trust_created", map[string]any{"site_id": site.ID, "trust_id": trust.ID, "repository_id": trust.RepositoryID})
	httpkit.WriteJSON(w, 201, trust)
}

func (s Service) RevokeOIDCTrust(w http.ResponseWriter, r *http.Request, account state.Account) {
	if !s.guard(w, account) {
		return
	}
	h, ok := s.oidcHost(w)
	if !ok {
		return
	}
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	if uuid.Validate(r.PathValue("trust")) != nil {
		failure(w, state.ErrNotFound)
		return
	}
	if err := h.HostingOIDCStore().RevokeHostingOIDCTrust(r.Context(), account.ID, site.ID, r.PathValue("trust")); err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), account.ID, "hosting_oidc_trust_revoked", map[string]any{"site_id": site.ID, "trust_id": r.PathValue("trust")})
	w.WriteHeader(204)
}

// ExchangeOIDC is anonymous but rate limited. No caller-provided issuer, JWKS
// URL, audience, account or operations can influence the verifier policy.
func (s Service) ExchangeOIDC(w http.ResponseWriter, r *http.Request) {
	h, ok := s.oidcHost(w)
	if !ok {
		return
	}
	var req api.HostingOIDCExchangeRequest
	if httpkit.DecodeJSONSized(r, &req, api.HostingOIDCRequestMaxBytes) != nil || uuid.Validate(req.SiteID) != nil || uuid.Validate(req.TrustID) != nil || req.Assertion == "" {
		oidcDenied(w)
		return
	}
	trust, err := h.HostingOIDCStore().GetHostingOIDCTrust(r.Context(), req.SiteID, req.TrustID)
	if err != nil {
		oidcDenied(w)
		return
	}
	account, err := h.HostingOIDCAccount(r.Context(), trust.AccountID)
	if err != nil || !account.Active() {
		oidcDenied(w)
		return
	}
	if !s.guard(w, account) {
		return
	}
	site, err := s.Host.HostingStore().GetHostingSite(r.Context(), account.ID, trust.SiteID)
	if err != nil || site.OriginID != s.Host.HostingOriginID() || site.Status != "active" || site.Suspended || !site.DurableRouting {
		oidcDenied(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingOIDCRequestTimeout)
	defer cancel()
	claims, err := s.OIDCVerifier.Verify(ctx, req.Assertion, hostingOIDCPolicy(trust))
	now := time.Now().UTC()
	if err != nil || claims == nil || claims.Subject == "" || claims.Issuer != "https://token.actions.githubusercontent.com" || !claims.Exp.After(now.Add(time.Second)) || claims.Exp.After(now.Add(api.HostingOIDCAssertionMaxTTL)) {
		oidcDenied(w)
		return
	}
	if _, ok = s.oidcHost(w); !ok {
		return
	}
	s.issueOIDCCredential(w, r.WithContext(ctx), h, trust, claims, now)
}

func hostingOIDCPolicy(t api.HostingOIDCTrust) *oidc.OIDCTrustPolicy {
	claims := map[string]string{"repository_owner_id": t.RepositoryOwnerID, "repository_id": t.RepositoryID, "repository": t.Repository, "ref": t.Ref, "workflow_ref": t.WorkflowRef, "event_name": t.EventName}
	if t.Environment != "" {
		claims["environment"] = t.Environment
	}
	return &oidc.OIDCTrustPolicy{AccountID: t.AccountID, IssuerURL: "https://token.actions.githubusercontent.com", JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks", Audience: []string{api.HostingOIDCAudience(t.SiteID)}, Algorithms: []string{"RS256"}, RequiredClaims: claims}
}

func (s Service) issueOIDCCredential(w http.ResponseWriter, r *http.Request, h OIDCHost, t api.HostingOIDCTrust, claims *oidc.Claims, now time.Time) {
	token, hash, err := api.GenerateHostingAutomationToken()
	if err != nil {
		failure(w, err)
		return
	}
	expires := now.Add(api.HostingOIDCBearerTTL)
	if claims.Exp.Before(expires) {
		expires = claims.Exp
	}
	c := api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: t.AccountID, SiteID: t.SiteID, OIDCTrustID: t.ID, Label: "GitHub OIDC", Operations: []string{"stage", "preview"}, Hash: hash, CreatedAt: now, ExpiresAt: expires}
	c, err = h.HostingOIDCStore().CreateHostingOIDCCredential(r.Context(), t, c)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), t.AccountID, "hosting_oidc_exchanged", map[string]any{"site_id": t.SiteID, "trust_id": t.ID, "credential_id": c.ID, "expires_at": c.ExpiresAt})
	c.Hash = nil
	httpkit.WriteJSON(w, 200, api.HostingAutomationCredentialResponse{Credential: c, Token: token})
}

func oidcDenied(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(401, api.CodeUnauthorized, "GitHub site identity rejected", "the job identity does not match an active site trust binding"))
}
