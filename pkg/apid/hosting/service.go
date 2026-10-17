// Package hosting is apid's internal-only static-site control surface.
package hosting

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

type Host interface {
	HostingEnabled() bool
	HostingDurableRoutingEnabled() bool
	AdminAllows(state.Account) (bool, *api.Problem)
	HostingStore() state.HostingStore
	HostingArtifacts() artifacts.ArtifactStore
	HostingDomain() string
	HostingOriginID() string
	HostingUploadBaseURL() string
	HostingProject(context.Context, string, string, string) (state.Project, error)
	HostingAudit(context.Context, string, string, map[string]any)
}

type Service struct {
	Host         Host
	OIDCVerifier oidc.Verifier
}

// Require must wrap idempotency replay as well as execution. Otherwise a
// previously successful response could bypass a subsequently disabled flag.
func (s Service) Require(next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if s.guard(w, acct) {
			next(w, r, acct)
		}
	}
}

func (s Service) guard(w http.ResponseWriter, acct state.Account) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.Host.HostingEnabled() {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "frontend_hosting_disabled", "Frontend hosting unavailable", "internal frontend hosting is disabled"))
		return false
	}
	if ok, p := s.Host.AdminAllows(acct); !ok {
		api.WriteProblem(w, p)
		return false
	}
	if s.Host.HostingStore() == nil || s.Host.HostingArtifacts() == nil || s.Host.HostingDomain() == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "frontend_hosting_unconfigured", "Frontend hosting unavailable", "hosting providers are not configured"))
		return false
	}
	return true
}

func failure(w http.ResponseWriter, err error) {
	p := api.NewProblem(http.StatusServiceUnavailable, "frontend_hosting_unavailable", "Frontend hosting unavailable", "the hosting operation could not complete; retry later")
	switch {
	case errors.Is(err, state.ErrNotFound):
		p = api.NewProblem(http.StatusNotFound, "not_found", "Not found", "no such hosting resource")
	case errors.Is(err, state.ErrHostingCancellationProtected):
		p = api.NewProblem(409, "hosting_ci_cancel_protected", "CI run is protected", "production releases, previously promoted releases and other preview references cannot be canceled; inspect the run and release before acting")
	case errors.Is(err, state.ErrConflict):
		p = api.NewProblem(http.StatusConflict, "hosting_release_conflict", "Hosting state changed", "refresh the site revision; only ready releases can be promoted and active or preview-referenced releases cannot be retired")
	case errors.Is(err, state.ErrInvalidArgument), errors.Is(err, artifacts.ErrInvalidArtifact):
		p = api.ErrValidation("invalid hosting manifest or uploaded artifact")
	case errors.Is(err, state.ErrHostingQuota):
		p = api.NewProblem(http.StatusConflict, "hosting_quota_exceeded", "Internal hosting quota reached", "retire unused releases before deploying again")
		var quota *state.HostingQuotaError
		if errors.As(err, &quota) {
			p.Limit, p.Observed = &quota.Limit, &quota.Observed
			if quota.Resource == "automation_credentials" {
				p.Code = "hosting_automation_credential_quota_exceeded"
				p.Detail = "revoke unused site automation credentials before issuing another"
			}
			if quota.Resource == "oidc_trusts" {
				p.Code = "hosting_oidc_trust_quota_exceeded"
				p.Detail = "revoke unused GitHub site trust bindings before creating another"
			}
			if quota.Resource == "oidc_sessions" {
				p.Code = "hosting_oidc_session_quota_exceeded"
				p.Detail = "wait for short-lived sessions to expire and reduce parallel GitHub jobs"
			}
			if quota.Resource == "ci_runs" {
				p.Code = "hosting_ci_run_quota_exceeded"
				p.Detail = "this internal site has reached its retained CI intent limit; retry an existing run or review the pilot lifecycle with an operator"
			}
			if quota.Resource == "previews" {
				p.Code = "hosting_preview_quota_exceeded"
				p.Detail = "preview rows and unconverted CI reservations fill capacity; retire unused CI releases or revoke previews and wait for safe cleanup, or let unused reservations expire"
			}
			if quota.Resource == "storage_bytes" {
				p.Code = "hosting_storage_quota_exceeded"
				p.Detail = "retained releases and pending uploads reserve storage until artifact cleanup succeeds"
			}
			if quota.Resource == "pending_uploads" {
				p.Code = "hosting_pending_upload_quota_exceeded"
				p.Detail = "finalize or retire a pending upload before starting another"
			}
		}
		p.DocsURL = "https://github.com/poyrazK/faas/blob/main/docs/internal-frontend-hosting.md"
	}
	api.WriteProblem(w, p)
}

func (s Service) site(w http.ResponseWriter, r *http.Request, acct state.Account) (api.HostingSite, bool) {
	if !s.guard(w, acct) {
		return api.HostingSite{}, false
	}
	id := r.PathValue("site")
	if uuid.Validate(id) != nil {
		failure(w, state.ErrNotFound)
		return api.HostingSite{}, false
	}
	site, err := s.Host.HostingStore().GetHostingSite(r.Context(), acct.ID, id)
	if err != nil {
		failure(w, err)
		return api.HostingSite{}, false
	}
	if site.OriginID != s.Host.HostingOriginID() {
		failure(w, errors.New("hosting origin configuration changed"))
		return api.HostingSite{}, false
	}
	return site, true
}

func (s Service) release(w http.ResponseWriter, r *http.Request, acct state.Account) (api.HostingRelease, bool) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return api.HostingRelease{}, false
	}
	id := r.PathValue("release")
	if uuid.Validate(id) != nil {
		failure(w, state.ErrNotFound)
		return api.HostingRelease{}, false
	}
	release, err := s.Host.HostingStore().GetHostingRelease(r.Context(), acct.ID, site.ID, id)
	if err != nil {
		failure(w, err)
		return api.HostingRelease{}, false
	}
	return release, true
}

func (s Service) CreateSite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.guard(w, acct) {
		return
	}
	var req api.CreateHostingSiteRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil || !artifacts.ValidName(req.Name) || !artifacts.ValidName(req.Environment) {
		api.WriteProblem(w, api.ErrValidation("project, environment, and a lowercase site name are required"))
		return
	}
	project, err := s.Host.HostingProject(r.Context(), acct.ID, req.Project, req.Environment)
	if err != nil {
		failure(w, err)
		return
	}
	id := uuid.NewString()
	site, err := s.Host.HostingStore().CreateHostingSite(r.Context(), api.HostingSite{ID: id, AccountID: acct.ID, ProjectID: project.ID, Environment: req.Environment, Name: req.Name, Hostname: id + "." + s.Host.HostingDomain(), OriginID: s.Host.HostingOriginID(), DurableRouting: s.Host.HostingDurableRoutingEnabled()})
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_site_created", map[string]any{"site_id": site.ID, "project_id": project.ID, "environment": req.Environment})
	httpkit.WriteJSON(w, http.StatusCreated, site)
}

func (s Service) ListSites(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.guard(w, acct) {
		return
	}
	rows, err := s.Host.HostingStore().ListHostingSites(r.Context(), acct.ID)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, rows)
}

func (s Service) GetSite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if site, ok := s.site(w, r, acct); ok {
		httpkit.WriteJSON(w, http.StatusOK, site)
	}
}

func (s Service) CreateRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateHostingReleaseRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.HostingReleaseRequestMaxBytes); err != nil || artifacts.ValidateManifest(req.HostingManifest) != nil || artifacts.ValidateProvenance(req.Provenance) != nil {
		api.WriteProblem(w, api.ErrValidation("a bounded static manifest with unique file paths, sizes, SHA-256 digests, content types, index.html, and valid optional configuration/provenance are required"))
		return
	}
	manifest := req.HostingManifest
	if !s.requireConfig(w, manifest) {
		return
	}
	release, ok := s.createReleaseIntent(w, r, acct, site.ID, req)
	if !ok {
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_release_created", map[string]any{"site_id": site.ID, "release_id": release.ID})
	httpkit.WriteJSON(w, http.StatusCreated, release)
}

func (s Service) ListReleases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	rows, err := s.Host.HostingStore().ListHostingReleases(r.Context(), acct.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, rows)
}

func (s Service) Uploads(w http.ResponseWriter, r *http.Request, acct state.Account) {
	release, ok := s.release(w, r, acct)
	if !ok {
		return
	}
	if release.State != "pending" {
		failure(w, state.ErrConflict)
		return
	}
	release, ok = s.touchRelease(w, r, acct, release)
	if !ok {
		return
	}
	result := api.HostingUploadResponse{Release: release, Uploads: []api.HostingUpload{}}
	for _, file := range release.Manifest.Files {
		upload, err := s.Host.HostingArtifacts().Upload(r.Context(), release.SiteID, release.ID, file)
		if err != nil {
			failure(w, err)
			return
		}
		result.Uploads = append(result.Uploads, upload)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, result)
}

func (s Service) Finalize(w http.ResponseWriter, r *http.Request, acct state.Account) {
	release, ok := s.release(w, r, acct)
	if !ok {
		return
	}
	if !s.requireConfig(w, release.Manifest) {
		return
	}
	if release.State == "ready" {
		httpkit.WriteJSON(w, http.StatusOK, release)
		return
	}
	if release.State != "pending" {
		failure(w, state.ErrConflict)
		return
	}
	release, ok = s.touchRelease(w, r, acct, release)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingFinalizeTimeout)
	defer cancel()
	if err := s.Host.HostingArtifacts().Publish(ctx, release.SiteID, release.ID, release.Manifest); err != nil {
		failure(w, err)
		return
	}
	// Re-check the switch after potentially slow storage calls. A disabled
	// pilot must not acknowledge new ready releases or promote them.
	if !s.guard(w, acct) || !s.requireConfig(w, release.Manifest) {
		return
	}
	release, err := s.Host.HostingStore().ReadyHostingRelease(ctx, acct.ID, release.SiteID, release.ID)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(ctx, acct.ID, "hosting_release_ready", map[string]any{"site_id": release.SiteID, "release_id": release.ID})
	httpkit.WriteJSON(w, http.StatusOK, release)
}

func (s Service) Promote(w http.ResponseWriter, r *http.Request, acct state.Account) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req api.PromoteHostingReleaseRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil || uuid.Validate(req.ReleaseID) != nil || req.ExpectedRevision < 1 {
		api.WriteProblem(w, api.ErrValidation("release_id and expected_revision are required"))
		return
	}
	release, err := s.Host.HostingStore().GetHostingRelease(r.Context(), acct.ID, site.ID, req.ReleaseID)
	if err != nil {
		failure(w, err)
		return
	}
	if !s.requireConfig(w, release.Manifest) {
		return
	}
	if s.historyEnabled() {
		history, allowed := s.historyHost(w, acct)
		if !allowed {
			return
		}
		site, err = history.PromoteHostingReleaseRecorded(r.Context(), acct.ID, site.ID, req.ReleaseID, req.ExpectedRevision, hostingActor(r, acct))
	} else {
		site, err = s.Host.HostingStore().PromoteHostingRelease(r.Context(), acct.ID, site.ID, req.ReleaseID, req.ExpectedRevision)
	}
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_release_promoted", map[string]any{"site_id": site.ID, "release_id": req.ReleaseID, "revision": site.Revision})
	httpkit.WriteJSON(w, http.StatusAccepted, site)
}

func (s Service) Retire(w http.ResponseWriter, r *http.Request, acct state.Account) {
	release, ok := s.release(w, r, acct)
	if !ok {
		return
	}
	release, err := s.Host.HostingStore().RetireHostingRelease(r.Context(), acct.ID, release.SiteID, release.ID)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_release_retired", map[string]any{"site_id": release.SiteID, "release_id": release.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, release)
}

func (s Service) DeleteSite(w http.ResponseWriter, r *http.Request, acct state.Account) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	site, err := s.Host.HostingStore().DeleteHostingSite(r.Context(), acct.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_site_deletion_requested", map[string]any{"site_id": site.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, site)
}

func (s Service) SetSuspended(w http.ResponseWriter, r *http.Request, acct state.Account) {
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	var req struct {
		Suspended        *bool `json:"suspended"`
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := httpkit.DecodeJSON(r, &req); err != nil || req.ExpectedRevision < 1 || req.Suspended == nil {
		api.WriteProblem(w, api.ErrValidation("suspended and expected_revision are required"))
		return
	}
	site, err := s.Host.HostingStore().SetHostingSiteSuspended(r.Context(), acct.ID, site.ID, req.ExpectedRevision, *req.Suspended)
	if err != nil {
		failure(w, err)
		return
	}
	s.Host.HostingAudit(r.Context(), acct.ID, "hosting_site_suspension_changed", map[string]any{"site_id": site.ID, "suspended": site.Suspended, "revision": site.Revision})
	httpkit.WriteJSON(w, http.StatusAccepted, site)
}
