// cmd/apid/handlers_cors_presets.go — CORS preset CRUD handlers
// (issue #975 #4 / PR-B / ADR-129). PR-A shipped the data model
// (migrations/00304) and the read path (pkg/state.Store
// ListCorsPresetsFor{Account,App} + GetCorsPresetByID); PR-B
// wires the customer-facing write surface:
//
//   - createCorsPreset → POST   /v1/cors-presets
//   - listCorsPresets  → GET    /v1/cors-presets
//   - getCorsPreset    → GET    /v1/cors-presets/{id}
//   - patchCorsPreset  → PATCH  /v1/cors-presets/{id}
//   - deleteCorsPreset → DELETE /v1/cors-presets/{id}
//
// The 402 plan-gate (ErrPlanCorsPresetsNotAllowed) fires BEFORE
// the create path touches the store, matching the
// ErrPlanAlertRulesNotAllowed precedent at
// handlers_alerts.go:158-162 — a Free customer never gets a
// quota-reached 403 because the per-plan cap is 0 and the
// feature is gated off entirely.
//
// The 403 quota-reached (ErrPlanCorsPresetQuotaReached) is
// rendered on a *state.CorsPresetQuotaError. The scope field
// distinguishes per-app from per-account so the customer sees
// the right copy ("delete from this app" vs "delete from any
// app on your account"). Mirrors the alert_rules pattern at
// handlers_alerts.go:174-179.
//
// The DELETE path is the FK ON DELETE SET NULL gateway: the
// preset is gone, edge_rules.cors_preset_id clears atomically
// via the trigger, the gatewayd-internal compile path fails
// closed (MergeCorsPresetIntoRule returns ErrNotFound) until
// the customer wires a new preset or inlines fallback values.
// See ADR-129 D1/D3 for the cascade contract.
//
// Each handler delegates to a private phase helper
// (decode+validate, load+gate, persist, audit) so the
// orchestrator stays under the CLAUDE.md ≤-50-lines handler
// ceiling. The handlers are JSON-only — there is no form-encoded
// variant (CORS presets are a dashboard / CLI surface, not a
// browser form).
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// createCorsPreset handles POST /v1/cors-presets. The plan-gate
// fires BEFORE the store is touched (Free customers see 402,
// never 403 quota-reached because the cap is 0). The body
// validation mirrors the inline kind=cors rule's Validate
// (same CorsOriginPattern regex, same *+credentials footgun,
// same MaxAgeSeconds bound). Audit event
// `cors_preset.created` carries the (account_id, preset_id,
// name, app_id) tuple.
func (s *server) createCorsPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if limits.CorsPresetsPerAccount == 0 {
		api.WriteProblem(w, api.ErrPlanCorsPresetsNotAllowed(acct.Plan))
		return
	}
	var req api.CreateCorsPresetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not decode body: "+err.Error()))
		return
	}
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := corsPresetWithinPlan(acct.Plan, limits, req.AllowOrigins, req.AllowMethods); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.gateCorsPresetApp(r.Context(), acct, req.AppID); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, prob := s.persistCreateCorsPreset(r.Context(), acct, req, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusCreated, corsPresetResponseFromRow(row))
}

// listCorsPresets handles GET /v1/cors-presets. Optional
// `app_id` query parameter filters to app-scoped presets only;
// absent = every preset the account owns (account-wide +
// app-scoped). The compile path unions the same set via
// ListCorsPresetsForAccount + ListCorsPresetsForApp; the apid
// surface mirrors the shape so the dashboard can render
// "account-wide" vs "app-scoped" badges without a second
// round trip.
func (s *server) listCorsPresets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var filter api.CorsPresetListFilter
	if v := r.URL.Query().Get("app_id"); v != "" {
		id := v
		filter.AppID = &id
	}
	if filter.AppID != nil {
		if prob := s.gateCorsPresetApp(r.Context(), acct, filter.AppID); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		rows, err := s.store.ListCorsPresetsForApp(r.Context(), acct.ID, *filter.AppID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list cors presets"))
			return
		}
		writeJSON(w, http.StatusOK, api.CorsPresetListResponse{
			Presets: corsPresetResponsesFromRows(rows),
		})
		return
	}
	rows, err := s.store.ListCorsPresetsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list cors presets"))
		return
	}
	writeJSON(w, http.StatusOK, api.CorsPresetListResponse{
		Presets: corsPresetResponsesFromRows(rows),
	})
}

// getCorsPreset handles GET /v1/cors-presets/{id}. Cross-tenant
// reads collapse to ErrNotFound (the pgstore WHERE clause pins
// account_id; the memstore mirror checks AccountID explicitly).
func (s *server) getCorsPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.GetCorsPresetByID(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load cors preset"))
		return
	}
	writeJSON(w, http.StatusOK, corsPresetResponseFromRow(row))
}

// patchCorsPreset handles PATCH /v1/cors-presets/{id}. The
// UpdateCorsPresetRequest.Validate enforces the partial-update
// invariants (at-least-one field, *+credentials footgun on
// the merged shape, MaxAgeSeconds bound, name length).
// The pgstore trigger fires pg_notify('cors_preset_changed',
// account_id) AFTER the UPDATE commits so the gatewayd-internal
// listener reloads the affected account's preset overlay
// (ADR-129 D4). Audit event `cors_preset.updated` carries the
// (account_id, preset_id, fields_changed) tuple.
func (s *server) patchCorsPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	var req api.UpdateCorsPresetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not decode body: "+err.Error()))
		return
	}
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Load the current row to merge with the partial update.
	// The handler-side merge keeps the contract: every field
	// the customer omits stays untouched (PCH nil-skip
	// convention matches UpdateEdgeRuleParams). A loaded row
	// is required to compute the post-update name+app_id
	// tuple for the *+credentials re-validation against the
	// post-update shape.
	existing, err := s.store.GetCorsPresetByID(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load cors preset"))
		return
	}
	merged, prob := mergeCorsPresetUpdate(existing, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Check only the lists this PATCH replaces, so a preset created
	// before the cap was enforced can still be renamed or re-scoped.
	if prob := corsPresetWithinPlan(acct.Plan, api.MustLimitsFor(acct.Plan), req.AllowOrigins, req.AllowMethods); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.gateCorsPresetApp(r.Context(), acct, appIDPtr(merged.AppID)); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.UpdateCorsPreset(r.Context(), acct.ID, id, merged)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account"))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Name already in use", "a cors preset with this name already exists for the (account, app) tuple"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update cors preset"))
		return
	}
	s.audit.Emit(r.Context(), "cors_preset.updated", &acct.ID, map[string]any{
		"preset_id":  row.ID,
		"account_id": row.AccountID,
		"app_id":     row.AppID,
		"name":       row.Name,
	})
	s.log.Info("cors preset updated",
		"preset", logsanitize.Field(row.ID),
		"account", acct.ID,
		"name", logsanitize.Field(row.Name),
	)
	writeJSON(w, http.StatusOK, corsPresetResponseFromRow(row))
}

// deleteCorsPreset handles DELETE /v1/cors-presets/{id}. The
// pgstore trigger fires pg_notify('cors_preset_changed',
// account_id) AFTER the DELETE commits so the gatewayd-internal
// listener reloads the affected account's preset overlay; the
// FK ON DELETE SET NULL clears edge_rules.cors_preset_id
// atomically with the preset's removal. Audit event
// `cors_preset.deleted` carries the (account_id, preset_id,
// name, app_id) tuple.
func (s *server) deleteCorsPreset(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	// Load first so the audit event can carry the (name, app_id)
	// tuple — after DELETE the row is gone.
	existing, err := s.store.GetCorsPresetByID(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load cors preset"))
		return
	}
	if err := s.store.DeleteCorsPreset(r.Context(), acct.ID, id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such cors preset", "no cors preset with that id is visible to this account"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete cors preset"))
		return
	}
	s.audit.Emit(r.Context(), "cors_preset.deleted", &acct.ID, map[string]any{
		"preset_id":  existing.ID,
		"account_id": existing.AccountID,
		"app_id":     existing.AppID,
		"name":       existing.Name,
	})
	s.log.Info("cors preset deleted",
		"preset", logsanitize.Field(existing.ID),
		"account", acct.ID,
		"name", logsanitize.Field(existing.Name),
	)
	w.WriteHeader(http.StatusNoContent)
}

// gateCorsPresetApp enforces the cross-tenant IDOR guard on
// AppID: an app-scoped preset must reference an app owned by
// the caller's account. A nil AppID (account-wide) skips the
// check. The 404 on miss matches the convention at
// handlers_alerts.go:243-247 (no slug-leak).
func (s *server) gateCorsPresetApp(ctx context.Context, acct state.Account, appID *string) *api.Problem {
	if appID == nil {
		return nil
	}
	app, err := s.store.AppByID(ctx, *appID)
	if err != nil || app.AccountID != acct.ID {
		return api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that id is visible to this account")
	}
	return nil
}

// appIDPtr is a small helper to convert a state.CorsPreset.AppID
// (string, "" = account-wide) into the *string the wire DTO
// uses (nil = account-wide, non-nil = app-scoped). Keeps the
// gate call sites readable.
func appIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// persistCreateCorsPreset wires the create path: build the
// state.CorsPreset, persist via CreateCorsPresetIfUnderQuota,
// emit the audit + structured log. Pulled out of
// createCorsPreset so the orchestrator stays under the
// CLAUDE.md handler ceiling.
func (s *server) persistCreateCorsPreset(ctx context.Context, acct state.Account, req api.CreateCorsPresetRequest, limits api.Limits) (state.CorsPreset, *api.Problem) {
	var appID string
	if req.AppID != nil {
		appID = *req.AppID
	}
	row, err := s.store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{
		AccountID:        acct.ID,
		AppID:            appID,
		Name:             req.Name,
		Description:      req.Description,
		AllowOrigins:     req.AllowOrigins,
		AllowMethods:     req.AllowMethods,
		AllowHeaders:     req.AllowHeaders,
		ExposeHeaders:    req.ExposeHeaders,
		AllowCredentials: req.AllowCredentials,
		MaxAgeSeconds:    req.MaxAgeSeconds,
	}, limits)
	if err != nil {
		var qe *state.CorsPresetQuotaError
		switch {
		case errors.As(err, &qe):
			return state.CorsPreset{}, api.ErrPlanCorsPresetQuotaReached(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed)
		case errors.Is(err, state.ErrNotFound):
			return state.CorsPreset{}, api.NewProblem(http.StatusNotFound, api.CodeValidation, "No such app", "no app with that id is visible to this account")
		case errors.Is(err, state.ErrConflict):
			return state.CorsPreset{}, api.NewProblem(http.StatusConflict, api.CodeValidation, "Name already in use", "a cors preset with this name already exists for the (account, app) tuple")
		default:
			return state.CorsPreset{}, api.ErrCapacity("could not create cors preset")
		}
	}
	s.audit.Emit(ctx, "cors_preset.created", &acct.ID, map[string]any{
		"preset_id":  row.ID,
		"account_id": row.AccountID,
		"app_id":     row.AppID,
		"name":       row.Name,
	})
	s.log.Info("cors preset created",
		"preset", logsanitize.Field(row.ID),
		"account", acct.ID,
		"name", logsanitize.Field(row.Name),
	)
	return row, nil
}

// mergeCorsPresetUpdate applies the partial update to the
// existing row. Every field is nil-skip (PCH convention) so
// the customer re-sends only the fields they want to change.
// The merged result is the input to UpdateCorsPreset. The
// *+credentials footgun re-validation against the merged
// shape is the only place the validate-after-merge runs
// (Validate ran on the partial body, but a partial body can
// carry AllowCredentials=true and omit AllowOrigins; the
// customer may have updated AllowOrigins separately, leaving
// the post-merge shape dangerous).
func mergeCorsPresetUpdate(existing state.CorsPreset, req api.UpdateCorsPresetRequest) (state.CorsPreset, *api.Problem) {
	out := existing
	if req.AppID != nil {
		if *req.AppID == nil {
			out.AppID = ""
		} else {
			out.AppID = **req.AppID
		}
	}
	if req.Name != nil {
		out.Name = *req.Name
	}
	if req.Description != nil {
		out.Description = *req.Description
	}
	if req.AllowOrigins != nil {
		out.AllowOrigins = req.AllowOrigins
	}
	if req.AllowMethods != nil {
		out.AllowMethods = req.AllowMethods
	}
	if req.AllowHeaders != nil {
		out.AllowHeaders = req.AllowHeaders
	}
	if req.ExposeHeaders != nil {
		out.ExposeHeaders = req.ExposeHeaders
	}
	if req.AllowCredentials != nil {
		out.AllowCredentials = *req.AllowCredentials
	}
	if req.MaxAgeSeconds != nil {
		out.MaxAgeSeconds = *req.MaxAgeSeconds
	}
	// Re-validate the merged shape against the *+credentials
	// footgun. The wire-level Validate ran on the partial
	// body; a partial body that omits AllowOrigins but sets
	// AllowCredentials=true would pass Validate (no
	// footgun-firing combination in the partial) yet produce
	// a dangerous merged shape if the existing row had
	// AllowOrigins=["*"].
	if out.AllowCredentials {
		for _, origin := range out.AllowOrigins {
			if origin == "*" {
				return state.CorsPreset{}, api.ErrValidation("cors preset cannot combine AllowCredentials: true with AllowOrigins: [\"*\"] (browsers reject this combination)")
			}
		}
	}
	return out, nil
}

// corsPresetResponseFromRow maps the state row to the wire
// response. The conversion handles the AppID *string nil-vs-
// empty distinction (account-wide → nil on the wire, app-
// scoped → &uuid).
func corsPresetResponseFromRow(row state.CorsPreset) api.CorsPresetResponse {
	var appID *string
	if row.AppID != "" {
		id := row.AppID
		appID = &id
	}
	return api.CorsPresetResponse{
		ID:               row.ID,
		AccountID:        row.AccountID,
		AppID:            appID,
		Name:             row.Name,
		Description:      row.Description,
		AllowOrigins:     row.AllowOrigins,
		AllowMethods:     row.AllowMethods,
		AllowHeaders:     row.AllowHeaders,
		ExposeHeaders:    row.ExposeHeaders,
		AllowCredentials: row.AllowCredentials,
		MaxAgeSeconds:    row.MaxAgeSeconds,
		CreatedAt:        row.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt:        row.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
}

func corsPresetResponsesFromRows(rows []state.CorsPreset) []api.CorsPresetResponse {
	out := make([]api.CorsPresetResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, corsPresetResponseFromRow(row))
	}
	return out
}

// corsPresetWithinPlan enforces CorsPresetMaxOrigins and
// CorsPresetMaxAllowMethods, which limits.go documents as enforced at
// this write boundary but nothing checked: a preset could carry as many
// origins as the request body held, and the gateway walks them on every
// matching request.
func corsPresetWithinPlan(plan api.Plan, limits api.Limits, origins, methods []string) *api.Problem {
	if len(origins) > limits.CorsPresetMaxOrigins {
		return api.ErrCorsPresetTooLarge(plan, "allow_origins", limits.CorsPresetMaxOrigins, len(origins))
	}
	if len(methods) > limits.CorsPresetMaxAllowMethods {
		return api.ErrCorsPresetTooLarge(plan, "allow_methods", limits.CorsPresetMaxAllowMethods, len(methods))
	}
	return nil
}
