package main

// ADR-126 / issue #975 item #2 — OpenAPI Import + Auto-Generation.
//
// Six app-scoped routes:
//
//   GET    /v1/apps/{slug}/openapi?source=manual_import|auto
//   POST   /v1/apps/{slug}/openapi                          (import)
//   POST   /v1/apps/{slug}/openapi/dry-run                  (suggestions)
//   GET    /v1/apps/{slug}/openapi/preview                  (contract + policy preview)
//   POST   /v1/apps/{slug}/openapi/apply                    (plan + explicit apply)
//   DELETE /v1/apps/{slug}/openapi
//
// All routes flow through authLimited → (requireMFA on writes) →
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
// rows for inspection. The apply route builds on the persisted
// document and requires an explicit preview hash before writing.
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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/openapiimport"
	"github.com/onebox-faas/faas/pkg/state"
)

// openAPIImportDialTimeout bounds the apid→gatewayd-internal
// observed-routes hop. Matches the existing routesDialTimeout
// contract (in-box, single-machine, fast).
const openAPIImportDialTimeout = 2 * time.Second

// openAPIImportEndpoint is the observed-routes URL the auto-gen
// reads from. Uses the existing /v1/internal/apps/{slug}/routes
// endpoint (cmd/gatewayd-internal/routes_handler.go) which
// returns the bounded route label set, not the
// pkg/gateway/control_routes.go shape from the pre-merge PR #1011
// draft (item #2 review-fix: the separate file was dropped on the
// rebuild branch because the production endpoint already serves
// the data apid needs; Count/P50MS/etc. fields default to zero
// for the auto-gen annotation since the per-route histogram
// surface lives in a separate /metrics scrape).
func openAPIImportEndpoint(gatewaydControlURL, appID string) string {
	return gatewaydControlURL + "/v1/internal/apps/" + appID + "/routes"
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

// getAppOpenAPIPolicyPreview returns a read-only declared-vs-observed route
// diff with the edge rules that match each route. It intentionally does not
// reuse the auto-generated document response: the preview keeps provenance
// and policy coverage explicit so a developer can decide what to change
// before any policy write.
func (s *server) getAppOpenAPIPolicyPreview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// Spec: API-hosting roadmap deliverable 11 / ADR-126 follow-up —
	// declared-vs-observed contract drift and route-policy preview.
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}

	raw, _, err := s.store.GetAppOpenAPIDoc(r.Context(), app.ID, app.AccountID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read imported OpenAPI document", err.Error()))
		return
	}

	var spec *openapidiff.Spec
	if len(raw) > 0 {
		spec, err = openapidiff.LoadBytes(raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "openapi_import_invalid",
				"persisted OpenAPI document could not be parsed", err.Error()))
			return
		}
	}

	observed, observedAvailable := s.fetchObservedRoutesWithStatus(r.Context(), app.ID)
	rules, err := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read app edge rules", err.Error()))
		return
	}

	routes := openapidiff.BuildRoutePolicyPreview(spec, observed, rules)
	outRoutes := make([]api.AppOpenAPIPolicyPreviewRoute, 0, len(routes))
	for _, route := range routes {
		outRules := make([]api.AppOpenAPIPolicyPreviewRule, 0, len(route.Rules))
		for _, rule := range route.Rules {
			outRules = append(outRules, api.AppOpenAPIPolicyPreviewRule{
				ID: rule.ID, MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
				MatchMethods: rule.MatchMethods, Priority: rule.Priority,
				Enabled: rule.Enabled, Kind: rule.Kind, ValidateMode: rule.ValidateMode,
				Action: rule.Action,
			})
		}
		outRoutes = append(outRoutes, api.AppOpenAPIPolicyPreviewRoute{
			Path: route.Path, Method: route.Method, Status: route.Status,
			Declared: route.Declared, Observed: route.Observed,
			Covered: route.Covered, Rules: outRules,
		})
	}

	source := "preview"
	if len(raw) == 0 {
		source = "empty: no_import"
	} else if !observedAvailable {
		source = "degraded: routes_unavailable"
	}
	resp := api.AppOpenAPIPolicyPreviewResponse{
		AppID: app.ID, Source: source, ObservedAvailable: observedAvailable,
		Routes: outRoutes,
	}
	if spec != nil {
		resp.OpenAPIVersion = spec.OpenAPIVersion()
	}
	if len(raw) > 0 {
		dryRun, dryErr := openapidiff.ComputeDryRun(raw, rules)
		if dryErr != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "openapi_import_invalid",
				"persisted OpenAPI document could not be analyzed", dryErr.Error()))
			return
		}
		resp.Suggestions = make([]api.EdgeRuleSuggestion, len(dryRun.Suggestions))
		for i, suggestion := range dryRun.Suggestions {
			resp.Suggestions[i] = api.EdgeRuleSuggestion{
				Path: suggestion.Path, Methods: suggestion.Methods,
				Kind: suggestion.Kind, Action: suggestion.Action,
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// postAppOpenAPIPolicyApply implements the explicit plan/apply workflow for
// OpenAPI-generated validation rules. The first call (confirm=false) is
// read-only and returns a deterministic preview_sha256. A mutation requires
// that exact token, so a concurrent document or rule change cannot be
// silently folded into an approval. Rules are created one at a time through
// the existing quota-checked store path; if a later write fails, rules
// created by this request are removed before the error is returned.
func (s *server) postAppOpenAPIPolicyApply(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	var req api.ApplyAppOpenAPIPolicyRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid OpenAPI policy apply request", "request body must be valid JSON"))
		return
	}

	raw, _, err := s.store.GetAppOpenAPIDoc(r.Context(), app.ID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no OpenAPI document imported for this app")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read imported OpenAPI document", err.Error()))
		return
	}
	rules, err := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read app edge rules", err.Error()))
		return
	}
	dryRun, err := openapidiff.ComputeDryRun(raw, rules)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "openapi_import_invalid",
			"persisted OpenAPI document could not be analyzed", err.Error()))
		return
	}
	suggestions := make([]api.EdgeRuleSuggestion, 0, len(dryRun.Suggestions))
	for _, suggestion := range dryRun.Suggestions {
		suggestions = append(suggestions, api.EdgeRuleSuggestion{
			Path: suggestion.Path, Methods: suggestion.Methods,
			Kind: suggestion.Kind, Action: suggestion.Action,
		})
	}
	matchHost := strings.ToLower(strings.TrimSpace(req.MatchHost))
	if matchHost == "" {
		matchHost = strings.ToLower(appHostForDomain(app.Slug, s.domain))
	}
	previewSHA256, err := openapidiff.PolicySuggestionsSHA256(matchHost, dryRun.Suggestions)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to fingerprint OpenAPI policy plan", err.Error()))
		return
	}
	resp := api.AppOpenAPIPolicyApplyResponse{
		AppID: app.ID, MatchHost: matchHost, PreviewSHA256: previewSHA256,
		Suggestions: suggestions, Planned: !req.Confirm,
		Applied: make([]api.EdgeRuleResponse, 0),
	}
	if !req.Confirm {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if req.PreviewSHA256 == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeOpenAPIPolicyConfirmationRequired,
			"OpenAPI policy confirmation is required", "set confirm=true together with the preview_sha256 returned by a plan request"))
		return
	}
	if req.PreviewSHA256 != previewSHA256 {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeOpenAPIPolicyStale,
			"OpenAPI policy plan is stale", "the OpenAPI document or edge rules changed; fetch a new plan before applying"))
		return
	}

	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
		return
	}
	created := make([]state.EdgeRule, 0, len(suggestions))
	rollback := func(ctx context.Context) {
		for i := len(created) - 1; i >= 0; i-- {
			row := created[i]
			if deleteErr := s.store.DeleteEdgeRule(ctx, row.ID); deleteErr == nil || errors.Is(deleteErr, state.ErrNotFound) {
				if s.notif != nil {
					_ = s.notif.Notify(ctx, db.NotifyEdgeRuleChanged,
						fmt.Sprintf(`{"app_id":%q,"rule_id":%q,"op":"deleted"}`, app.ID, row.ID))
				}
			}
		}
	}
	for _, suggestion := range suggestions {
		// Dry-run suggestions expose the kind-tagged action union so the
		// dashboard can render it. The edge-rule create endpoint accepts
		// the kind-specific action body itself (for validate, `schema` and
		// `validate_mode` are top-level), so unwrap the matching member
		// before passing it through the existing validator.
		actionPayload := any(suggestion.Action)
		if nested, exists := suggestion.Action[suggestion.Kind]; exists {
			actionPayload = nested
		}
		actionRaw, marshalErr := json.Marshal(actionPayload)
		if marshalErr != nil {
			rollback(r.Context())
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
				"failed to encode OpenAPI policy action", marshalErr.Error()))
			return
		}
		createReq := api.CreateEdgeRuleRequest{
			MatchHost: matchHost, MatchPath: suggestion.Path,
			MatchMethods: suggestion.Methods, Kind: suggestion.Kind,
			ValidateMode: api.ValidateModeObserve, Action: actionRaw,
		}
		if prob := validateEdgeRuleBody(&createReq, acct.Plan); prob != nil {
			rollback(r.Context())
			api.WriteProblem(w, prob)
			return
		}
		row, createErr := s.store.CreateEdgeRuleIfUnderQuota(r.Context(), state.CreateEdgeRuleParams{
			AccountID: acct.ID, AppID: app.ID, MatchHost: matchHost,
			MatchPath: suggestion.Path, MatchMethods: suggestion.Methods,
			Priority: 100, Enabled: true, Kind: state.EdgeRuleKind(suggestion.Kind),
			Action: actionFromBody(suggestion.Kind, actionRaw), ValidateMode: api.ValidateModeObserve,
		}, limits)
		if createErr != nil {
			rollback(r.Context())
			var qe *state.EdgeRuleQuotaError
			switch {
			case errors.As(createErr, &qe):
				if qe.PerKind {
					api.WriteProblem(w, api.ErrPlanEdgeRuleKindQuotaReached(acct.Plan, qe.Kind, qe.Limit, qe.Observed))
				} else {
					api.WriteProblem(w, api.ErrPlanLimitEdgeRules(acct.Plan, qe.Limit, qe.Observed))
				}
			case errors.Is(createErr, state.ErrNotFound):
				s.notFound(w, "no such app")
			case errors.Is(createErr, state.ErrConflict):
				api.WriteProblem(w, api.ErrEdgeRuleConflict(createErr.Error()))
			default:
				api.WriteProblem(w, api.ErrCapacity("could not apply OpenAPI policy"))
			}
			return
		}
		created = append(created, row)
		if s.notif != nil {
			_ = s.notif.Notify(r.Context(), db.NotifyEdgeRuleChanged,
				fmt.Sprintf(`{"app_id":%q,"rule_id":%q,"op":"created"}`, app.ID, row.ID))
		}
		s.audit.Emit(r.Context(), "edge_rule.created", &acct.ID, map[string]any{
			auditKeyRuleID: row.ID, auditKeyAppID: row.AppID,
			auditKeyMatchHost: row.MatchHost, auditKeyMatchPath: row.MatchPath,
			auditKeyMatchMethods: row.MatchMethods, auditKeyPriority: row.Priority,
			auditKeyEnabled: row.Enabled, auditKeyKind: row.Kind,
			"source": "openapi_policy_apply",
		})
	}
	resp.Planned = false
	resp.Applied = make([]api.EdgeRuleResponse, 0, len(created))
	for _, row := range created {
		resp.Applied = append(resp.Applied, edgeRuleResponse(row))
	}
	resp.AppliedCount = len(created)
	s.audit.Emit(r.Context(), "app.openapi_policy.applied", &acct.ID, map[string]any{
		"app_id": app.ID, "match_host": matchHost, "preview_sha256": previewSHA256,
		"applied_count": len(created),
	})
	writeJSON(w, http.StatusOK, resp)
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
//
// Cache hit path (review-fix): the SHA inputs are cheap to
// compute without running GenerateFromApp, so the handler
// looks the cache first. On a hit, the pre-rendered body +
// source string are written verbatim and the heavy pipeline
// is skipped entirely (issue #975 item #2 D4 +
// pre-rendered-payload review-fix).
// writeAutoSpecHeaders is the shared response-header recipe for
// the auto-gen path (cache hit + cache miss + empty-import). The
// three arms all set the same Content-Type, Cache-Control, and
// X-OpenAPI-Doc-Source fields; only the X-Faas-Cache value (hit
// vs miss) and the annotations-count string differ.
func writeAutoSpecHeaders(w http.ResponseWriter, source, cacheState string, annotationsCount int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Faas-Cache", cacheState)
	w.Header().Set("X-OpenAPI-Doc-Source", source)
	w.Header().Set("X-OpenAPI-Doc-Annotations-Count", fmt.Sprintf("%d", annotationsCount))
}

// loadAutoGenInputs gathers (doc, observed, rules, SHAs) for the
// auto-gen path. Returns ok=false with a problem written to w on
// failure. The cache-key SHAs are computed O(n) over the raw
// inputs so a cache hit can skip parse + merge + render;
// GenerateFromApp recomputes the same SHAs internally and
// equality is pinned by pkg/openapidiff tests.
func (s *server) loadAutoGenInputs(w http.ResponseWriter, r *http.Request, app state.App) (
	doc []byte, observed []openapidiff.RouteRow, rules []state.EdgeRule,
	docSHA, routesSHA, rulesSHA [32]byte, ok bool,
) {
	var docErr error
	doc, _, docErr = s.store.GetAppOpenAPIDoc(r.Context(), app.ID, app.AccountID)
	if docErr != nil && !errors.Is(docErr, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read imported doc", docErr.Error()))
		return nil, nil, nil, [32]byte{}, [32]byte{}, [32]byte{}, false
	}
	observed = s.fetchObservedRoutes(r.Context(), app.ID)
	var rulesErr error
	rules, rulesErr = s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if rulesErr != nil {
		s.log.Debug("getAppOpenAPI ListEdgeRulesForApp", "err", rulesErr.Error())
		rules = nil
	}
	if len(doc) > 0 {
		docSHA = openapidiff.SumSHA256(doc)
	}
	if len(observed) > 0 {
		routesSHA = openapidiff.HashRoutes(observed)
	}
	if len(rules) > 0 {
		rulesSHA = openapidiff.HashRules(rules)
	}
	return doc, observed, rules, docSHA, routesSHA, rulesSHA, true
}

func (s *server) serveOpenAPIDocAuto(w http.ResponseWriter, r *http.Request, app state.App) {
	doc, observed, rules, docSHA, routesSHA, rulesSHA, ok := s.loadAutoGenInputs(w, r, app)
	if !ok {
		return
	}
	// Cheap path: cache hit. Skip parse + merge + render
	// entirely; write the pre-rendered body and headers.
	if s.specCache != nil {
		if hit, ok := s.specCache.Get(app.ID, docSHA, routesSHA, rulesSHA); ok {
			writeAutoSpecHeaders(w, hit.Source, "hit", hit.AnnotationsCount)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(hit.Body)
			return
		}
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
			writeAutoSpecHeaders(w, openapidiff.SourceEmptyImportRules, "miss", 0)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","info":{"title":"","version":""},"paths":{}}`))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to generate OpenAPI doc", genErr.Error()))
		return
	}
	rendered := renderOpenAPISpecJSON(genSpec, genMeta, app)
	if s.specCache != nil {
		s.specCache.Put(app.ID, genMeta.DocSHA256, genMeta.RoutesSHA256, genMeta.RulesSHA256,
			rendered, genMeta.Source, len(genMeta.Annotations), time.Now())
	}
	writeAutoSpecHeaders(w, genMeta.Source, "miss", len(genMeta.Annotations))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rendered)
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
	if len(spec.Components) > 0 {
		schemas := make(map[string]any, len(spec.Components))
		for name, schema := range spec.Components {
			schemas[name] = renderSchemaJSON(schema)
		}
		out["components"] = map[string]any{"schemas": schemas}
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
		if pi == nil {
			continue
		}
		methods := cloneOpenAPIObject(pi.Raw)
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
	out := cloneOpenAPIObject(op.Raw)
	if len(op.Responses) > 0 {
		responses := map[string]any{}
		for code, r := range op.Responses {
			if r == nil {
				continue
			}
			response := cloneOpenAPIObject(r.Raw)
			content := map[string]any{}
			for ct, sch := range r.Content {
				if sch == nil {
					continue
				}
				content[ct] = map[string]any{"schema": renderSchemaJSON(sch)}
			}
			if _, ok := response["description"]; !ok {
				response["description"] = "OK"
			}
			response["content"] = content
			responses[code] = response
		}
		out["responses"] = responses
	}
	return out
}

func renderSchemaJSON(schema *openapidiff.Schema) any {
	if schema == nil {
		return nil
	}
	if len(schema.Raw) > 0 {
		return cloneOpenAPIObject(schema.Raw)
	}
	return schema
}

func cloneOpenAPIObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// fetchObservedRoutes calls out to the gatewayd-internal
// /v1/internal/apps/{appID}/routes bridge (the production
// endpoint added in PR #1026, item #2 review-fix). Returns nil
// on any failure so GenerateFromApp degrades gracefully
// (Source: "degraded: routes_unavailable"). The wire shape
// is {Slug, AppID, Routes []string, CapHit bool}; we synthesise
// RouteRow from each label with Count/P50/P95/P99/ErrorPct
// zero (the per-route histogram surface lives in a separate
// /metrics scrape, not on this control-listener endpoint).
func (s *server) fetchObservedRoutes(ctx context.Context, appID string) []openapidiff.RouteRow {
	rows, _ := s.fetchObservedRoutesWithStatus(ctx, appID)
	return rows
}

func (s *server) fetchObservedRoutesWithStatus(ctx context.Context, appID string) ([]openapidiff.RouteRow, bool) {
	if s.gatewaydControlURL == "" {
		return nil, false
	}
	dialCtx, cancel := context.WithTimeout(ctx, openAPIImportDialTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dialCtx, http.MethodGet, openAPIImportEndpoint(s.gatewaydControlURL, appID), nil)
	if err != nil {
		return nil, false
	}
	client := &http.Client{Timeout: openAPIImportDialTimeout}
	resp, err := client.Do(req)
	if err != nil {
		s.log.Debug("apid→gatewayd observed-routes dial failed", "err", err.Error(), "app_id", appID)
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var env struct {
		Slug   string   `json:"slug"`
		AppID  string   `json:"app_id"`
		Routes []string `json:"routes"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, false
	}
	rows := make([]openapidiff.RouteRow, 0, len(env.Routes))
	for _, label := range env.Routes {
		rows = append(rows, openapidiff.RouteRow{Route: label})
	}
	return rows, true
}

// readAndValidateImportBody is the shared body-read + size-cap +
// meta-schema-validate + endpoint-cap helper used by both the
// import and dry-run handlers. On any reject path it writes the
// RFC 7807 problem to w and returns ok=false; the caller bails.
// Returns the validated raw doc, the parsed openapi_version, and
// the endpoint count on success.
func (s *server) readAndValidateImportBody(w http.ResponseWriter, r *http.Request) (raw []byte, version string, endpointCount int, ok bool) {
	maxRead := int64(state.OpenAPIImportMaxDocBytes) + 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxRead)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap",
			fmt.Sprintf("limit=%d", state.OpenAPIImportMaxDocBytes)))
		return nil, "", 0, false
	}
	if len(body) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "empty_body",
			"request body is empty", ""))
		return nil, "", 0, false
	}
	if len(body) > state.OpenAPIImportMaxDocBytes {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "openapi_import_too_large",
			"imported doc exceeds size cap",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxDocBytes, len(body))))
		return nil, "", 0, false
	}
	v, n, vErr := openapiimport.ValidateImport(body)
	if vErr != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_invalid",
			"imported OpenAPI doc failed validation", vErr.Error()))
		return nil, "", 0, false
	}
	if n > state.OpenAPIImportMaxEndpoints {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "openapi_import_too_many_endpoints",
			"imported doc declares too many endpoints",
			fmt.Sprintf("limit=%d observed=%d", state.OpenAPIImportMaxEndpoints, n)))
		return nil, "", 0, false
	}
	return body, v, n, true
}

// postAppOpenAPIImport handles POST /v1/apps/{slug}/openapi.
// Reads the body, validates, runs the per-account quota gate
// (atomic count+lock+upsert at the store), emits audit +
// pg_notify, returns 200.
func (s *server) postAppOpenAPIImport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	raw, openapiVersion, endpointCount, ok := s.readAndValidateImportBody(w, r)
	if !ok {
		return
	}
	if err := s.enforceOpenAPIImportQuota(w, r, acct, app, raw, endpointCount, openapiVersion); err != nil {
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

// enforceOpenAPIImportQuota runs the per-account quota gate +
// atomic upsert. The Count+check+Upsert triplet is bundled
// inside UpsertAppOpenAPIDocIfUnderQuota so a TOCTOU race
// between two concurrent imports can't slip past the cap; the
// upfront Count here is a fast-feedback pre-check that lets us
// return the 403 BEFORE the JSONB INSERT round-trip. Writes the
// RFC 7807 problem to w on any reject path. Returns nil on
// success.
func (s *server) enforceOpenAPIImportQuota(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, raw []byte, endpointCount int, openapiVersion string) error {
	count, err := s.store.CountOpenAPIImportsByAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to count imports", err.Error()))
		return err
	}
	planMax := acct.Plan.OpenAPIImportsPerAccount()
	if planMax == 0 {
		// Fail-closed: unknown plans (or plans explicitly set
		// to 0 — e.g., a tier-down migration) cannot import.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "openapi_import_quota_reached",
			"per-account OpenAPI import quota reached",
			fmt.Sprintf("limit=%d observed=%d", planMax, count)))
		return state.ErrQuotaExceeded
	}
	if count >= planMax {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "openapi_import_quota_reached",
			"per-account OpenAPI import quota reached",
			fmt.Sprintf("limit=%d observed=%d", planMax, count)))
		return state.ErrQuotaExceeded
	}
	if err := s.store.UpsertAppOpenAPIDocIfUnderQuota(r.Context(), app.ID, acct.ID, raw, endpointCount, openapiVersion, planMax); err != nil {
		var qe *state.QuotaError
		switch {
		case errors.As(err, &qe) && qe.Kind == state.QuotaErrorKindOpenAPIImports:
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "openapi_import_quota_reached",
				"per-account OpenAPI import quota reached",
				fmt.Sprintf("limit=%d observed=%d", qe.Limit, qe.Observed)))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
				"failed to persist import", err.Error()))
		}
		return err
	}
	return nil
}

// postAppOpenAPIImportDryRun handles POST
// /v1/apps/{slug}/openapi/dry-run. Read-only; no persist.
// Reads + validates the body, returns EdgeRuleSuggestion rows
// for (path, method) pairs not already covered by an existing
// validate edge rule.
func (s *server) postAppOpenAPIImportDryRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	raw, _, _, ok := s.readAndValidateImportBody(w, r)
	if !ok {
		return
	}
	existing, rulesErr := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if rulesErr != nil {
		// The dry-run path is best-effort: a missing rules view
		// shouldn't block the preview, but a silent swallow
		// (pre-fix) left failures invisible. Surface at Debug
		// so the tripwire (slog JSON → log shipper) catches a
		// future regression — mirror of the sibling
		// serveOpenAPIDocAuto path above.
		s.log.Debug("postAppOpenAPIImportDryRun ListEdgeRulesForApp", "err", rulesErr.Error())
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
