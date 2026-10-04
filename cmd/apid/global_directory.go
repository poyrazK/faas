package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureGlobalDirectory(ctx context.Context, enabled bool, pool *pgxpool.Pool) error {
	if !enabled {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("apid: global directory requires a PostgreSQL pool")
	}
	directory := globaldirectory.NewPgStore(pool)
	if err := directory.CheckScope(ctx); err != nil {
		return fmt.Errorf("apid: global directory: %w", err)
	}
	s.globalDirectory = directory
	return nil
}

func (s *server) registerGlobalDirectoryRoutes(mux *http.ServeMux) {
	read := func(h accountHandler) http.HandlerFunc {
		return s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.requireGlobalDirectoryOwner(false, h))))
	}
	write := func(h accountHandler) http.HandlerFunc {
		return s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireGlobalDirectoryOwner(true, s.requireVerifiedEmail(h)))))
	}
	mux.HandleFunc("GET /v1/global-cells", read(s.listGlobalCells))
	mux.HandleFunc("PUT /v1/global-cells/{id}", s.authLimited(s.requireAdminMutation(s.putGlobalCell)))
	mux.HandleFunc("GET /v1/global-apps", read(s.listGlobalApps))
	mux.HandleFunc("POST /v1/global-apps", write(s.createGlobalApp))
	mux.HandleFunc("GET /v1/global-apps/{slug}", read(s.getGlobalApp))
	mux.HandleFunc("PUT /v1/global-apps/{slug}/runtime-quotas/{allocation_id}", write(s.requireGlobalTargetConsent(s.putGlobalRuntimeQuota)))
	mux.HandleFunc("GET /v1/global-apps/{slug}/runtime-quotas/{allocation_id}", read(s.getGlobalRuntimeQuota))
	mux.HandleFunc("DELETE /v1/global-apps/{slug}/runtime-quotas/{allocation_id}", write(s.requireGlobalTargetConsent(s.retireGlobalRuntimeQuota)))
	mux.HandleFunc("PUT /v1/global-apps/{slug}/image-releases/{release_id}", write(s.requirePersonalGlobalTarget(s.putGlobalImageRelease)))
	mux.HandleFunc("GET /v1/global-apps/{slug}/image-releases/{release_id}", read(s.getGlobalImageRelease))
	mux.HandleFunc("PUT /v1/global-apps/{slug}/targets/{region}", write(s.requireGlobalTargetConsent(s.attachGlobalAppTarget)))
	mux.HandleFunc("DELETE /v1/global-apps/{slug}/targets/{region}", write(s.requireGlobalTargetConsent(s.detachGlobalAppTarget)))
	mux.HandleFunc("GET /v1/global-apps/{slug}/targets/{region}/resolve", read(s.resolveGlobalApp))
	cellDeploy := s.requireRegionalAccountKey(s.requireDirectoryAccount(s.requireVerifiedEmail(s.requireCellDeploymentApp(s.idempotent(s.createDeployment)))))
	mux.HandleFunc("POST /v1/cells/{cell_id}/apps/{app_id}/deployments", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(cellDeploy))))
	cellRelease := func(h accountHandler) accountHandler {
		return s.requireRegionalReleasePrincipal(s.requireVerifiedEmail(s.requireCellDeploymentApp(h)))
	}
	mux.HandleFunc("PUT /v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeDeployWrite, api.ScopeRegionalImageRelease)(cellRelease(s.putCellImageRelease)))))
	mux.HandleFunc("GET /v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeRegionalImageRelease)(cellRelease(s.getCellImageRelease)))))
	mux.HandleFunc("GET /v1/cell/apps/{slug}/binding", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeRegionalImageRelease)(s.requireVerifiedEmail(s.requireRegionalBindingApp(s.regionalBindingResponse))))))
	grantRoute := func(h accountHandler) accountHandler {
		return s.requireVerifiedEmail(s.requireRegionalGrantApp(h))
	}
	grantAdmin := grantRoute(s.putRegionalReleaseGrant)
	mux.HandleFunc("PUT /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin)(grantAdmin))))
	mux.HandleFunc("GET /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeRegionalImageRelease)(grantRoute(s.getRegionalReleaseGrant)))))
	mux.HandleFunc("DELETE /v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeRegionalImageRelease)(grantRoute(s.revokeRegionalReleaseGrant)))))
}

func (s *server) globalDirectoryAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.globalDirectory == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalAppDirectoryUnavailable, "Global app directory unavailable", "The global app directory is not enabled on this control plane."))
		return false
	}
	return true
}

func decodeGlobalDirectoryRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.GlobalDirectoryRequestMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid directory request", "Send exactly one bounded JSON object with the documented fields."))
		return false
	}
	return true
}

func (s *server) globalDirectoryError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	switch {
	case errors.Is(err, globaldirectory.ErrOwnerInactive):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Directory owner inactive", "An active organization is required for directory access."))
	case errors.Is(err, globaldirectory.ErrOwnerForbidden):
		api.WriteProblem(w, api.ErrOrgRoleForbidden("global directory access"))
	case errors.Is(err, globaldirectory.ErrOwnerPlanChanged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization plan changed", "Retry registration using the current organization plan."))
	case errors.Is(err, globaldirectory.ErrOrgTargetsUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Shared runtime admission unavailable", "Organization identities and bindings are available. Image releases require global runtime quota and regional organization billing admission."))
	case errors.Is(err, globaldirectory.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Directory identity not found", "No matching global app or regional binding exists for this tenant."))
	case errors.Is(err, globaldirectory.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Directory identity conflicts", "The global name or regional identity is already bound; existing identities cannot be replaced."))
	case errors.Is(err, globaldirectory.ErrStaleCell):
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalAppTargetReverificationRequired, "Regional binding requires verification", "The selected cell is disabled or changed. Re-verify the binding once it is enabled."))
	case errors.Is(err, globaldirectory.ErrVerification):
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeGlobalAppTargetVerificationFailed, "Regional ownership could not be verified", "The regional app must belong to the supplied account-admin key in the registered cell."))
	case errors.Is(err, globaldirectory.ErrLimit):
		apps, readErr := s.globalDirectory.ListApps(r.Context(), globalOwnerID(r, acct))
		if readErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read global identity count"))
			return
		}
		api.WriteProblem(w, api.ErrPlanLimitApps(api.MustLimitsFor(globalOwner(r, acct).Plan), len(apps)))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not access the global app directory"))
	}
}

func globalCellResponse(cell globaldirectory.Cell) api.GlobalCellResponse {
	return api.GlobalCellResponse{ID: cell.ID, Region: cell.Region, APIBase: cell.APIBase, Enabled: cell.Enabled, Generation: cell.Generation}
}

func globalTargetResponse(target globaldirectory.Target) api.GlobalAppTargetResponse {
	status := "verified"
	if !target.CellEnabled {
		status = "cell_disabled"
	} else if !target.Resolvable() {
		status = "reverify_required"
	}
	return api.GlobalAppTargetResponse{CellID: target.CellID, Region: target.Region, CellGeneration: target.CellGeneration, RegionalAccountID: target.RegionalAccountID, RegionalOrgID: target.RegionalOrgID, RegionalAppID: target.RegionalAppID, RegionalAppSlug: target.RegionalAppSlug, VerifiedAt: target.VerifiedAt, ResolutionStatus: status}
}

func (s *server) globalAppResponse(r *http.Request, app globaldirectory.App) (api.GlobalAppResponse, error) {
	targets, err := s.globalDirectory.ListTargets(r.Context(), app.OwnerID, app.Slug)
	if err != nil {
		return api.GlobalAppResponse{}, err
	}
	out := api.GlobalAppResponse{ID: app.ID, Slug: app.Slug, RoutingPolicy: app.RoutingPolicy, Owner: globalOwnerResponse(globalOwner(r, state.Account{ID: app.OwnerID}).Owner), CreatedAt: app.CreatedAt, Targets: make([]api.GlobalAppTargetResponse, 0, len(targets))}
	for _, target := range targets {
		out.Targets = append(out.Targets, globalTargetResponse(target))
	}
	return out, nil
}

func (s *server) listGlobalCells(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	cells, err := s.globalDirectory.ListCells(r.Context())
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	out := make([]api.GlobalCellResponse, 0, len(cells))
	for _, cell := range cells {
		out = append(out, globalCellResponse(cell))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) putGlobalCell(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	var req api.PutGlobalCellRequest
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	cell := globaldirectory.Cell{ID: r.PathValue("id"), Region: req.Region, APIBase: req.APIBase}
	if req.Enabled == nil || cell.Validate() != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid cell registry entry", "Use a canonical UUID and region, an HTTPS API origin and an explicit enabled boolean."))
		return
	}
	cell.Enabled = *req.Enabled
	cell, err := s.globalDirectory.PutCell(r.Context(), cell)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_cell.updated", &acct.ID, map[string]any{"cell_id": cell.ID, "region": cell.Region, "generation": cell.Generation, "enabled": cell.Enabled})
	writeJSON(w, http.StatusOK, globalCellResponse(cell))
}

func (s *server) createGlobalApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	var req api.CreateGlobalAppRequest
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	if !globaldirectory.ValidSlug(req.Slug) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid global slug", "Use a canonical app slug."))
		return
	}
	app, err := s.createGlobalDirectoryApp(r, acct, req.Slug)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	out, err := s.globalAppResponse(r, app)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_app.registered", &acct.ID, map[string]any{"global_app_id": app.ID, "slug": app.Slug, "routing_policy": app.RoutingPolicy})
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) listGlobalApps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	apps, err := s.globalDirectory.ListApps(r.Context(), globalOwnerID(r, acct))
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	out := make([]api.GlobalAppResponse, 0, len(apps))
	for _, app := range apps {
		response, err := s.globalAppResponse(r, app)
		if err != nil {
			s.globalDirectoryError(w, r, acct, err)
			return
		}
		out = append(out, response)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getGlobalApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	app, err := s.globalDirectory.AppBySlug(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"))
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	out, err := s.globalAppResponse(r, app)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) attachGlobalAppTarget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	slug, region := r.PathValue("slug"), r.PathValue("region")
	if _, err := s.globalDirectory.AppBySlug(r.Context(), globalOwnerID(r, acct), slug); err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	var req api.AttachGlobalAppTargetRequest
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	if !globaldirectory.ValidSlug(req.RegionalAppSlug) || !api.ValidAPIKeyFormat(req.RegionalAPIKey) || region == "" || !api.ValidComputeRegion(region) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid regional binding", "Supply a canonical region and app slug, and a regional account-admin API key."))
		return
	}
	binding, err := s.verifyGlobalBinding(r, region, req)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	target, err := s.commitGlobalTarget(r, acct, binding)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_app.target_attached", &acct.ID, map[string]any{"global_app_id": target.GlobalAppID, "cell_id": target.CellID, "region": target.Region, "regional_app_id": target.RegionalAppID})
	writeJSON(w, http.StatusOK, globalTargetResponse(target))
}

func (s *server) verifyGlobalBinding(r *http.Request, region string, req api.AttachGlobalAppTargetRequest) (globaldirectory.Binding, error) {
	cell, err := s.globalDirectory.CellByRegion(r.Context(), region)
	if err != nil {
		return globaldirectory.Binding{}, err
	}
	if !cell.Enabled {
		return globaldirectory.Binding{}, globaldirectory.ErrStaleCell
	}
	return s.globalVerifier.Verify(r.Context(), cell, req.RegionalAppSlug, req.RegionalAPIKey)
}

func (s *server) detachGlobalAppTarget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	if err := s.removeGlobalTarget(r, acct); err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_app.target_detached", &acct.ID, map[string]any{"slug": r.PathValue("slug"), "region": r.PathValue("region")})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) resolveGlobalApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	target, err := globaldirectory.Resolve(r.Context(), s.globalDirectory, globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("region"))
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	writeJSON(w, http.StatusOK, api.GlobalAppResolutionResponse{GlobalAppID: target.GlobalAppID, CellID: target.CellID, Region: target.Region, APIBase: target.APIBase, RegionalAppID: target.RegionalAppID, RegionalAppSlug: target.RegionalAppSlug, CellGeneration: target.CellGeneration})
}
