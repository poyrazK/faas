package main

// ADR-126 / issue #975 item #2 — OpenAPI Import + Auto-Generation.
//
// Four app-scoped routes:
//
//   GET    /v1/apps/{slug}/openapi?source=manual_import|auto
//   POST   /v1/apps/{slug}/openapi                          (import)
//   POST   /v1/apps/{slug}/openapi/dry-run                  (suggestions)
//   DELETE /v1/apps/{slug}/openapi
//
// All four flow through authLimited → (requireMFA on writes) →
// requireScope → loadApp. The GET surface is read-only and
// accepts two source modes:
//
//   ?source=manual_import — returns the customer's uploaded
//     doc verbatim (mirrors item #1's /deployments/{dep}/openapi
//     but on the app-keyed table, ADR-126 D1).
//
//   ?source=auto — runs pkg/openapidiff.GenerateFromApp with
//     the imported doc + observed routes (from the ADR-093
//     bridge just shipped) + existing edge rules; the merged
//     spec is cached for 5 min keyed on (app_id, sha(doc),
//     sha(routes), sha(rules)) and invalidated by either
//     pg_notify channel (item #2 D5).
//
// The dry-run route takes the same body shape as the import
// route but does NOT persist — it returns EdgeRuleSuggestion
// rows the customer pastes into the existing create-edge-rule
// endpoint (item #2 D3).
//
// Plan-tier gate is gone for these surfaces — every plan
// including Free can import (item #2 D6: limits are abuse-
// surface, not tier). The per-plan cap is enforced via
// state.OpenAPIImportMaxDocBytes + state.OpenAPIImportMaxEndpoints
// (constant across plans).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapiimport"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// openAPIImportDialTimeout bounds the apid→gatewayd-internal
// route-rows hop. Matches the existing routesDialTimeout
// contract (in-box, single-machine, fast).
const openAPIImportDialTimeout = 2 * time.Second

// openAPIImportEndpoint is the route-rows URL the auto-gen
// reads observed routes from. The slug is the app's UUID,
// not the human slug, so the bridge can skip the apps
// table round-trip.
func openAPIImportEndpoint(gatewaydControlURL, appID string) string {
	return gatewaydControlURL + "/v1/internal/apps/" + appID + "/route-rows"
}

// getAppOpenAPI handles GET /v1/apps/{slug}/openapi.
//
// Two modes (selected via ?source=):
//   - source=manual_import (default): return the persisted
//     customer doc. 200 with the body + Cache-Control.
//   - source=auto: run GenerateFromApp, return the merged
//     spec. 200 with Source="auto" or the degraded source
//     string. Cache hit returns X-Faas-Cache: hit.
//
// source=dry_run is reserved (POST-only; dry-run as a GET would
// require a body). 405 in that case.
func (s *server) getAppOpenAPI(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "manual_import"
	}
	switch source {
	case "dry_run":
		api.WriteProblem(w, api.NewProblem(http.StatusMethodNotAllowed, "dry_run_requires_post",
			"dry-run is POST-only; use POST /v1/apps/{slug}/openapi/dry-run", ""))
		return
	case "manual_import":
		s.serveOpenAPIDocManualImport(w, r, app)
	case "auto":
		s.serveOpenAPIDocAuto(w, r, app)
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "invalid_source",
			"source must be one of: manual_import, auto",
			fmt.Sprintf("observed=%s", source)))
		return
	}
}

// serveOpenAPIDocManualImport returns the persisted customer
// doc verbatim. Mirrors the deployment-keyed getOpenAPIDoc
// (item #1) shape but on the app-keyed table.
func (s *server) serveOpenAPIDocManualImport(w http.ResponseWriter, r *http.Request, app state.App) {
	doc, meta, err := s.store.GetAppOpenAPIDoc(r.Context(), app.ID, app.AccountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no OpenAPI document imported for this app")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read OpenAPI document", err.Error()))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-OpenAPI-Doc-Source", meta.Source)
	w.Header().Set("X-OpenAPI-Doc-Byte-Size", fmt.Sprintf("%d", meta.ByteSize))
	w.Header().Set("X-OpenAPI-Doc-Version", meta.OpenAPIVersion)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}

// serveOpenAPIDocAuto runs pkg/openapidiff.GenerateFromApp
// with the three input streams (imported doc, observed
// routes, edge rules), looks the cache, and serves the
// generated spec. The cache is invalidated by either
// pg_notify channel (subscriber wired in
// openapi_doc_subscriber.go).
func (s *server) serveOpenAPIDocAuto(w http.ResponseWriter, r *http.Request, app state.App) {
	doc, _, docErr := s.store.GetAppOpenAPIDoc(r.Context(), app.ID, app.AccountID)
	if docErr != nil && !errors.Is(docErr, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read imported doc", docErr.Error()))
		return
	}
	observed := s.fetchObservedRoutes(r.Context(), app.ID)
	rules, rulesErr := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if rulesErr != nil {
		s.log.Debug("getAppOpenAPI ListEdgeRulesForApp", "err", rulesErr.Error())
		rules = nil
	}
	genSpec, genMeta, genErr := openapidiff.GenerateFromApp(openapidiff.GenerateFromAppInputs{
		AppID:          app.ID,
		AccountID:      app.AccountID,
		ImportedDoc:    doc,
		ObservedRoutes: observed,
		EdgeRules:      rules,
	})
	if genErr != nil {
		if errors.Is(genErr, openapidiff.ErrImportMissing) {
			// 200 with empty paths + source marker so the
			// dashboard can render the "no spec yet" state.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-OpenAPI-Doc-Source", openapidiff.SourceEmptyImportRules)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","info":{"title":"","version":""},"paths":{}}`))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to generate OpenAPI doc", genErr.Error()))
		return
	}
	source := genMeta.Source
	cacheHit := false
	if s.specCache != nil {
		if hit, ok := s.specCache.Get(app.ID, genMeta.DocSHA256, genMeta.RoutesSHA256, genMeta.RulesSHA256); ok {
			genSpec = hit.Spec
			cacheHit = true
			source = openapidiff.SourceAuto
		} else {
			s.specCache.Put(app.ID, genMeta.DocSHA256, genMeta.RoutesSHA256, genMeta.RulesSHA256,
				genSpec, time.Now())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if cacheHit {
		w.Header().Set("X-Faas-Cache", "hit")
	} else {
		w.Header().Set("X-Faas-Cache", "miss")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-OpenAPI-Doc-Source", source)
	w.Header().Set("X-OpenAPI-Doc-Annotations-Count", fmt.Sprintf("%d", len(genMeta.Annotations)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(renderOpenAPISpecJSON(genSpec, genMeta, app))
}

// renderOpenAPISpecJSON is the small helper that emits the
// *Spec + per-operation annotations to the wire JSON. The
// pkg/openapidiff package stays schema-shape-only; the apid
// handler is the bridge that joins annotations into the
// rendered JSON. Falls back to an empty-spec stub when the
// spec is nil (defensive — GenerateFromApp returns nil only
// for the ErrImportMissing case which is handled earlier).
func renderOpenAPISpecJSON(spec *openapidiff.Spec, genMeta openapidiff.GenerateFromAppMeta, app state.App) []byte {
	if spec == nil {
		return []byte(`{"openapi":"3.1.0","info":{"title":"","version":""},"paths":{}}`)
	}
	out := map[string]any{
		"openapi": spec.OpenAPIVersion(),
		"info":    map[string]any{"title": app.Slug, "version": "1"},
		"paths":   renderPathsJSON(spec.Paths),
	}
	if len(genMeta.Annotations) > 0 {
		out["x-faas-edge-rules"] = genMeta.Annotations
	}
	b, err := json.Marshal(out)
	if err != nil {
		return []byte(`{"openapi":"3.1.0","info":{"title":"","version":""},"paths":{}}`)
	}
	return b
}

// renderPathsJSON converts the *PathItem.Methods map into the
// standard OpenAPI 3.x wire shape (paths.<path>.<method>).
func renderPathsJSON(paths map[string]*openapidiff.PathItem) map[string]any {
	out := map[string]any{}
	for pathKey, pi := range paths {
		if pi == nil || len(pi.Methods) == 0 {
			continue
		}
		methods := map[string]any{}
		for method, op := range pi.Methods {
			if op == nil {
				continue
			}
			methods[method] = renderOperationJSON(op)
		}
		out[pathKey] = methods
	}
	return out
}

// renderOperationJSON emits the minimum wire shape per
// operation. The pkg/openapidiff loader only emits the
// schema-shape fields (Responses.{Content.{Schema.{Type}}});
// the bridge leaves the rest to the wire marshaller.
func renderOperationJSON(op *openapidiff.Operation) map[string]any {
	out := map[string]any{}
	if len(op.Responses) > 0 {
		responses := map[string]any{}
		for code, r := range op.Responses {
			if r == nil {
				continue
			}
			content := map[string]any{}
			for ct, sch := range r.Content {
				if sch == nil {
					continue
				}
				content[ct] = map[string]any{"schema": sch}
			}
			responses[code] = map[string]any{
				"description": "OK",
				"content":     content,
			}
		}
		out["responses"] = responses
	}
	return out
}

// fetchObservedRoutes calls out to the gatewayd-internal
// /v1/internal/apps/{appID}/route-rows bridge. Returns nil
// on any failure so GenerateFromApp degrades gracefully
// (Source: "degraded: routes_unavailable").
func (s *server) fetchObservedRoutes(ctx context.Context, appID string) []openapidiff.RouteRow {
	if s.gatewaydControlURL == "" {
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, openAPIImportDialTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dialCtx, http.MethodGet, openAPIImportEndpoint(s.gatewaydControlURL, appID), nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: openAPIImportDialTimeout}
	resp, err := client.Do(req)
	if err != nil {
		s.log.Debug("apid→gatewayd route-rows dial failed", "err", err.Error(), "app_id", appID)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var env struct {
		Slug   string                 `json:"slug"`
		AppID  string                 `json:"app_id"`
		Routes []openapidiff.RouteRow `json:"routes"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil
	}
	return env.Routes
}

// postAppOpenAPIImport handles POST /v1/apps/{slug}/openapi.
// Reads the body, validates (size + endpoint count + schema),
// checks per-account quota, persists via UpsertAppOpenAPIDoc,
// emits audit + pg_notify, returns 200.
func (s *server) postAppOpenAPIImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	maxRead := int64(state.OpenAPIImportMaxDocBytes) + 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxRead)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap",
			fmt.Sprintf("limit=%d", state.OpenAPIImportMaxDocBytes)))
		return
	}
	if len(raw) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "empty_body",
			"request body is empty", ""))
		return
	}
	if len(raw) > state.OpenAPIImportMaxDocBytes {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxDocBytes, len(raw))))
		return
	}
	openapiVersion, endpointCount, validateErr := openapiimport.ValidateImport(raw)
	if validateErr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_invalid",
			"imported OpenAPI doc failed validation", validateErr.Error()))
		return
	}
	if endpointCount > state.OpenAPIImportMaxEndpoints {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_too_many_endpoints",
			"imported doc declares too many endpoints",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxEndpoints, endpointCount)))
		return
	}
	count, err := s.store.CountOpenAPIImportsByAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to count imports", err.Error()))
		return
	}
	planMax := acct.Plan.OpenAPIImportsPerAccount()
	if planMax == 0 {
		// Fail-closed: unknown plans (or plans explicitly set
		// to 0 — e.g., a tier-down migration) cannot import.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "openapi_import_quota_reached",
			"per-account OpenAPI import quota reached",
			fmt.Sprintf("limit=%d observed=%d", planMax, count)))
		return
	}
	if count >= planMax {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "openapi_import_quota_reached",
			"per-account OpenAPI import quota reached",
			fmt.Sprintf("limit=%d observed=%d", planMax, count)))
		return
	}
	if err := s.store.UpsertAppOpenAPIDoc(r.Context(), app.ID, acct.ID, raw, endpointCount, openapiVersion); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to persist import", err.Error()))
		return
	}
	s.audit.Emit(r.Context(), "app.openapi_import.replaced", &acct.ID, map[string]any{
		"app_id":          app.ID,
		"openapi_version": openapiVersion,
		"endpoint_count":  endpointCount,
		"byte_size":       len(raw),
	})
	if s.notif != nil {
		_ = s.notif.Notify(r.Context(), db.NotifyAppOpenAPIDocChanged,
			fmt.Sprintf(`{"app_id":%q,"op":"replaced"}`, app.ID))
	}
	_, gotMeta, err := s.store.GetAppOpenAPIDoc(r.Context(), app.ID, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read back import", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_id":          app.ID,
		"source":          gotMeta.Source,
		"openapi_version": gotMeta.OpenAPIVersion,
		"endpoint_count":  gotMeta.EndpointCount,
		"byte_size":       gotMeta.ByteSize,
		"captured_at":     gotMeta.CapturedAt,
		"updated_at":      gotMeta.UpdatedAt,
	})
}

// postAppOpenAPIImportDryRun handles POST
// /v1/apps/{slug}/openapi/dry-run. Read-only; no persist.
// Reads the body, validates, returns EdgeRuleSuggestion rows
// for (path, method) pairs not already covered by an existing
// validate edge rule.
func (s *server) postAppOpenAPIImportDryRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	maxRead := int64(state.OpenAPIImportMaxDocBytes) + 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxRead)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap", ""))
		return
	}
	if len(raw) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "empty_body",
			"request body is empty", ""))
		return
	}
	if len(raw) > state.OpenAPIImportMaxDocBytes {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxDocBytes, len(raw))))
		return
	}
	_, endpointCount, validateErr := openapiimport.ValidateImport(raw)
	if validateErr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_invalid",
			"imported OpenAPI doc failed validation", validateErr.Error()))
		return
	}
	if endpointCount > state.OpenAPIImportMaxEndpoints {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_too_many_endpoints",
			"imported doc declares too many endpoints",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxEndpoints, endpointCount)))
		return
	}
	existing, rulesErr := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if rulesErr != nil {
		existing = nil
	}
	dryRun, dryErr := openapidiff.ComputeDryRun(raw, existing)
	if dryErr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_invalid",
			"failed to walk imported doc for dry-run", dryErr.Error()))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"app_id":          app.ID,
		"openapi_version": dryRun.OpenAPIVersion,
		"endpoint_count":  dryRun.EndpointCount,
		"suggestions":     dryRun.Suggestions,
	})
}

// deleteAppOpenAPIImport handles DELETE /v1/apps/{slug}/openapi.
// Idempotent: 204 even if no row existed.
func (s *server) deleteAppOpenAPIImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	if err := s.store.DeleteAppOpenAPIDoc(r.Context(), app.ID, acct.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to delete OpenAPI import", err.Error()))
		return
	}
	s.audit.Emit(r.Context(), "app.openapi_import.deleted", &acct.ID, map[string]any{
		"app_id": app.ID,
	})
	if s.notif != nil {
		_ = s.notif.Notify(r.Context(), db.NotifyAppOpenAPIDocChanged,
			fmt.Sprintf(`{"app_id":%q,"op":"deleted"}`, app.ID))
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON is provided by cmd/apid/server.go:2311 — this file
// uses that shared helper to keep the Content-Type + status +
// encoding pattern in one place.