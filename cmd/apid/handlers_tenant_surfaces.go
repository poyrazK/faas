// handlers_tenant_surfaces.go — apid HTTP handlers for the
// tenant-surfaces customer-facing surface (issue #879 / ADR-100
// PR-C). Mirrors cmd/apid/handlers_ext.go::createDomain /
// listDomains / deleteDomain (the closest precedent). Routes
// register in cmd/apid/server.go; CLI surface lives in
// cmd/gregale/commands_tenant_surfaces.go.
//
// The feature flag FAAS_TENANT_SURFACES_ENABLED gates every
// handler — the cluster ships dark until the cert-engine
// real-mint ADR lands. Plan-tier gating (Free=403, Hobby/Pro/
// Scale=200) is enforced by the store surface via
// limits.TenantSurfacesAllowed (PR-A land).
//
// All state-changing handlers emit an audit row + a pg_notify
// on NotifyTenantSurfaceChanged. The notify is what
// gatewayd-internal's cert engine listens for (PR-A land:
// cmd/gatewayd-internal/backend.go:271) — a customer adds a
// hostname, the trigger fires, gatewayd re-issues the SAN
// bundle.
package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// createTenantSurface — POST /v1/apps/{slug}/tenant-surfaces.
// Decodes a CreateTenantSurfaceRequest, resolves the parent app
// via loadApp (the slug-scoped helper that enforces the
// app.AccountID == acct.ID predicate), creates the surface under
// the per-account quota, then attaches each seed hostname under
// the per-surface quota. Both quota errors surface as RFC 7807
// problem codes so the dashboard renders the upgrade copy.
//
// Returns 202 Accepted (not 201) — the surface row is in a
// pending/active state but the cert engine has to mint. Mirrors
// createDomain's 202 choice (the legacy custom_domains path
// returns 202 for the same reason).
func (s *server) createTenantSurface(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateTenantSurfaceRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.AppID == "" || req.Name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", "app_id and name are required"))
		return
	}
	if req.AppID != app.ID {
		s.notFound(w, "no such app")
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.TenantSurfacesAllowed {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	surf, hostnames, problem := s.createSurfaceUnderQuotas(r, acct, app, req, limits)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// pg_notify on tenant_surface_changed is emitted by the trigger
	// at migrations/00243:127-145 on every relevant row change
	// (surface + hostname INSERT/UPDATE/DELETE). The payload is
	// the bare surface UUID — what the cert-engine listener in
	// cmd/gatewayd-internal/backend.go:542 parses. An explicit
	// notif.Notify here would fire a SECOND event with the wrong
	// payload shape and triple-count the surface in the dashboard.
	// dns_poller.go:85-88 follows the same pattern.
	s.log.Info("tenant surface created",
		"surface", surf.ID, "name", logsanitize.Field(surf.Name),
		"app", app.ID, "account", acct.ID, "hostnames", len(hostnames))
	s.audit.Emit(r.Context(), "tenant_surface.added", &acct.ID, map[string]any{
		"surface_id": surf.ID,
		"app_id":     app.ID,
		"name":       surf.Name,
		"cert_kind":  string(surf.CertKind),
		"hostnames":  hostnameStrings(hostnames),
	})
	writeJSON(w, http.StatusAccepted, surfaceResponseWithHostnames(surf, hostnames))
}

// createSurfaceUnderQuotas wraps the two quota-gated inserts
// (surface + N hostnames) so createTenantSurface stays under the
// 50-line CLAUDE.md handler cap. Returns the created surface +
// the verified-or-pending hostname rows + a non-nil problem on
// any failure (the caller writes the problem and bails).
func (s *server) createSurfaceUnderQuotas(
	r *http.Request,
	acct state.Account,
	app state.App,
	req api.CreateTenantSurfaceRequest,
	limits api.Limits,
) (state.TenantSurface, []state.TenantHostname, *api.Problem) {
	ctx := r.Context()
	certKind := state.CertKind(req.CertKind)
	if certKind == "" {
		certKind = state.CertKindPerHostSAN
	}
	// Validate cert_kind BEFORE the store call. Without this, a
	// bogus value reaches the SQL CHECK constraint and surfaces as
	// ErrInvalidArgument, which the handler's surfaceCreateProblem
	// maps to ErrCapacity (500) instead of the dedicated
	// tenant_surface_cert_kind_invalid (400) the dashboard
	// expects. The PR-A-reserved code (pkg/api/errors.go:2043)
	// is the customer-facing signal that the field is malformed.
	if !certKind.Valid() {
		return state.TenantSurface{}, nil, api.ErrTenantSurfaceCertKindInvalid(string(certKind))
	}
	surf, err := s.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID,
		AppID:     app.ID,
		Name:      req.Name,
		CertKind:  certKind,
	}, limits)
	if err != nil {
		return state.TenantSurface{}, nil, surfaceCreateProblem(err, acct.Plan)
	}
	hostnames := make([]state.TenantHostname, 0, len(req.Hostnames))
	for _, h := range req.Hostnames {
		hn := strings.ToLower(strings.TrimSpace(h))
		if hn == "" {
			continue
		}
		created, err := s.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
			SurfaceID:      surf.ID,
			Hostname:       hn,
			ChallengeToken: randomToken(16),
		}, limits)
		if err != nil {
			// Best-effort cleanup: roll back the surface so the
			// caller doesn't see a half-created row. Soft-delete
			// is the same path PR-A's Txn-aware caller uses.
			_ = s.store.DeleteTenantSurface(ctx, surf.ID)
			return state.TenantSurface{}, nil, hostnameCreateProblem(err, acct.Plan, surf.ID)
		}
		hostnames = append(hostnames, created)
	}
	return surf, hostnames, nil
}

// surfaceCreateProblem maps a CreateTenantSurfaceIfUnderQuota
// error to an RFC 7807 problem. Distinct from hostnameCreateProblem
// because the quota codes differ (surface vs hostname) and the
// 404-on-missing-account case is its own branch.
func surfaceCreateProblem(err error, plan api.Plan) *api.Problem {
	var qe *state.TenantSurfaceQuotaError
	switch {
	case errors.As(err, &qe):
		return api.ErrTenantSurfaceQuota(plan, qe.Limit, qe.Observed)
	case errors.Is(err, state.ErrTenantSurfacesNotAllowed):
		return api.ErrTenantSurfacesNotAllowed(plan)
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeValidation,
			"Account missing", "parent account not found")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Surface name conflict", "a surface with this name already exists for this account")
	default:
		return api.ErrCapacity("could not create tenant surface")
	}
}

// hostnameCreateProblem maps a CreateTenantHostnameIfUnderQuota
// error to an RFC 7807 problem. The AlreadyClaimed case uses the
// PR-A-reserved code so the dashboard renders a distinct copy
// from the per-surface quota trip.
func hostnameCreateProblem(err error, plan api.Plan, surfaceID string) *api.Problem {
	var qe *state.TenantHostnameQuotaError
	switch {
	case errors.As(err, &qe):
		return api.ErrTenantHostnameQuota(plan, surfaceID, qe.Limit, qe.Observed)
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeTenantHostnameAlreadyClaimed,
			"Hostname claimed", "this hostname is already attached to another surface")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeValidation,
			"Surface missing", "parent surface not found")
	default:
		return api.ErrCapacity("could not add tenant hostname")
	}
}

// listTenantSurfaces — GET /v1/apps/{slug}/tenant-surfaces.
// Returns every ACTIVE surface on the app (one app can hold
// multiple surfaces per the D1 "one surface ↔ one app" decision
// — the cardinality is surfaces-per-account, not surfaces-per-app).
// Soft-deleted surfaces are filtered out — the cert history /
// audit trail stay in the DB but the API hides them. The list
// is bounded by TenantSurfacesPerAccount (25 today) so we render
// the whole list with no cursor — mirrors listCrons / listDomains.
func (s *server) listTenantSurfaces(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	surfaces, err := s.store.ListTenantSurfacesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant surfaces"))
		return
	}
	out := make([]api.TenantSurfaceResponse, 0, len(surfaces))
	for _, surf := range surfaces {
		if surf.Status == state.SurfaceStatusDeleted {
			continue
		}
		hostnames, herr := s.store.ListTenantHostnamesForSurface(r.Context(), surf.ID)
		if herr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list tenant hostnames"))
			return
		}
		out = append(out, surfaceResponseWithHostnames(surf, hostnames))
	}
	writeJSON(w, http.StatusOK, api.ListTenantSurfacesResponse{Surfaces: out})
}

// getTenantSurface — GET /v1/apps/{slug}/tenant-surfaces/{id}.
// Single surface + its hostnames. The id is a UUID; we
// re-resolve through the store so we can apply the
// AccountID == acct.ID check (the surface row carries
// AccountID; we don't need to hop through the app again).
// Soft-deleted surfaces return 404 — the route is read-only
// for active surfaces; the cert history / audit trail stay
// in the DB but the API hides them.
func (s *server) getTenantSurface(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	id := r.PathValue("id")
	surf, err := s.store.GetTenantSurfaceByID(r.Context(), id)
	if err != nil || surf.AccountID != acct.ID || surf.Status == state.SurfaceStatusDeleted {
		s.notFound(w, "no such surface")
		return
	}
	hostnames, herr := s.store.ListTenantHostnamesForSurface(r.Context(), surf.ID)
	if herr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant hostnames"))
		return
	}
	writeJSON(w, http.StatusOK, surfaceResponseWithHostnames(surf, hostnames))
}

// deleteTenantSurface — DELETE /v1/apps/{slug}/tenant-surfaces/{id}.
// Soft-delete (PR-A: DeleteTenantSurface flips status to 'deleted'
// so the audit + cert_history paths keep referencing the row).
// We also cascade-delete the hostnames — the surface is gone
// from a routing perspective; orphan hostnames serve no
// purpose and trip the UQ on a future re-add. The notify fires
// so gatewayd drops any in-flight cert work. Soft-deleted
// surfaces are NOT re-deletable (404 — same as missing).
func (s *server) deleteTenantSurface(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	id := r.PathValue("id")
	surf, err := s.store.GetTenantSurfaceByID(r.Context(), id)
	if err != nil || surf.AccountID != acct.ID || surf.Status == state.SurfaceStatusDeleted {
		s.notFound(w, "no such surface")
		return
	}
	// Cascade hostnames first so a future re-add of the same
	// hostname to a new surface doesn't trip ErrConflict. The
	// first failure (list OR per-row delete) is propagated to
	// the caller as a 500 — a partial-cascade leaves orphan
	// hostname rows that block the re-add via the global UQ
	// (migrations/00243:99). Surface is NOT deleted if the
	// cascade fails; the operator can retry the DELETE.
	hostnames, err := s.store.ListTenantHostnamesForSurface(r.Context(), surf.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list tenant hostnames for cascade"))
		return
	}
	for _, h := range hostnames {
		if err := s.store.DeleteTenantHostname(r.Context(), h.Hostname); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not cascade delete tenant hostname"))
			return
		}
	}
	if err := s.store.DeleteTenantSurface(r.Context(), surf.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete tenant surface"))
		return
	}
	// No explicit notify: the trigger at migrations/00243:127-145
	// fires on the surface + each hostname DELETE.
	s.log.Info("tenant surface deleted",
		"surface", surf.ID, "app", surf.AppID, "account", acct.ID)
	s.audit.Emit(r.Context(), "tenant_surface.removed", &acct.ID, map[string]any{
		"surface_id": surf.ID,
		"app_id":     surf.AppID,
		"name":       surf.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// addTenantHostname — POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames.
// Decodes AddTenantHostnameRequest, lowercases + trims the
// hostname, creates the row under the per-surface quota. The
// global UQ on tenant_hostnames.hostname means a hostname
// already attached to ANOTHER surface returns
// ErrConflict → 409 tenant_hostname_already_claimed (PR-A
// reserves CodeTenantHostnameAlreadyClaimed for exactly this
// case).
func (s *server) addTenantHostname(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	id := r.PathValue("id")
	surf, err := s.store.GetTenantSurfaceByID(r.Context(), id)
	if err != nil || surf.AccountID != acct.ID || surf.Status == state.SurfaceStatusDeleted {
		s.notFound(w, "no such surface")
		return
	}
	var req api.AddTenantHostnameRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	if hostname == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", "hostname is required"))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("unknown plan"))
		return
	}
	if ws, ok := s.store.(state.CustomDomainWildcardStore); ok {
		if wildcard, werr := ws.WildcardDomainForHost(r.Context(), hostname); werr == nil {
			api.WriteProblem(w, api.ErrWildcardDomainTenantSurfaceOverlap(wildcard.Domain, hostname))
			return
		} else if !errors.Is(werr, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not check wildcard-domain overlap"))
			return
		}
	}
	h, err := s.store.CreateTenantHostnameIfUnderQuota(r.Context(), state.CreateTenantHostnameParams{
		SurfaceID:      surf.ID,
		Hostname:       hostname,
		ChallengeToken: randomToken(16),
	}, limits)
	if err != nil {
		api.WriteProblem(w, hostnameCreateProblem(err, acct.Plan, surf.ID))
		return
	}
	// No explicit notify: the trigger at migrations/00243:127-145
	// fires on the hostname INSERT.
	s.log.Info("tenant hostname added",
		"hostname", logsanitize.Field(h.Hostname),
		"surface", surf.ID, "account", acct.ID)
	s.audit.Emit(r.Context(), "tenant_hostname.added", &acct.ID, map[string]any{
		"surface_id": surf.ID,
		"hostname":   h.Hostname,
	})
	writeJSON(w, http.StatusAccepted, hostnameResponse(h))
}

// removeTenantHostname — DELETE /v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{hostname}.
// Looks up the hostname through the join (so we get the
// surface_id back), confirms AccountID, then deletes. The path
// param is lowercased server-side to match the citext storage.
func (s *server) removeTenantHostname(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	if _, ok := s.loadApp(w, r, acct, r.PathValue("slug")); !ok {
		return
	}
	surfID := r.PathValue("id")
	hostname := strings.ToLower(r.PathValue("hostname"))
	h, err := s.store.GetTenantHostnameByName(r.Context(), hostname)
	if err != nil {
		s.notFound(w, "no such hostname")
		return
	}
	if h.SurfaceID != surfID {
		s.notFound(w, "no such hostname")
		return
	}
	surf, err := s.store.GetTenantSurfaceByID(r.Context(), surfID)
	if err != nil || surf.AccountID != acct.ID {
		s.notFound(w, "no such hostname")
		return
	}
	if err := s.store.DeleteTenantHostname(r.Context(), hostname); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete tenant hostname"))
		return
	}
	// No explicit notify: the trigger at migrations/00243:127-145
	// fires on the hostname DELETE.
	s.log.Info("tenant hostname removed",
		"hostname", logsanitize.Field(hostname),
		"surface", surf.ID, "account", acct.ID)
	s.audit.Emit(r.Context(), "tenant_hostname.removed", &acct.ID, map[string]any{
		"surface_id": surf.ID,
		"hostname":   hostname,
	})
	w.WriteHeader(http.StatusNoContent)
}

// surfaceResponseWithHostnames renders a surface + its
// hostnames as a TenantSurfaceResponse. Hostnames list is
// embedded (not cursor'd) because the per-surface quota caps
// the dataset at TenantHostnamesPerSurface (250 today).
func surfaceResponseWithHostnames(surf state.TenantSurface, hostnames []state.TenantHostname) api.TenantSurfaceResponse {
	out := api.TenantSurfaceResponse{
		ID:            surf.ID,
		AccountID:     surf.AccountID,
		AppID:         surf.AppID,
		Name:          surf.Name,
		CertKind:      string(surf.CertKind),
		Status:        string(surf.Status),
		CertState:     string(surf.CertState),
		CertNotAfter:  surf.CertNotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		CertLastError: surf.CertLastError,
		CreatedAt:     surf.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     surf.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Hostnames:     make([]api.TenantHostnameResponse, 0, len(hostnames)),
	}
	for _, h := range hostnames {
		out.Hostnames = append(out.Hostnames, hostnameResponse(h))
	}
	return out
}

// hostnameResponse renders one hostname row. Verified is the
// boolean shortcut (mirrors CustomDomain.Verified()); the
// dashboard flips the row from "pending" (yellow) to "verified"
// (green) based on this field.
func hostnameResponse(h state.TenantHostname) api.TenantHostnameResponse {
	var verifiedAt string
	if h.Verified() {
		verifiedAt = h.VerifiedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return api.TenantHostnameResponse{
		Hostname:       h.Hostname,
		ChallengeToken: h.ChallengeToken,
		Verified:       h.Verified(),
		VerifiedAt:     verifiedAt,
		LastError:      h.LastError,
		TXTRecord:      "_faas-verify." + h.Hostname,
	}
}

// hostnameStrings is a tiny helper for the audit emit; the
// audit data shape wants []string not []TenantHostname.
func hostnameStrings(hostnames []state.TenantHostname) []string {
	out := make([]string, len(hostnames))
	for i, h := range hostnames {
		out[i] = h.Hostname
	}
	return out
}
