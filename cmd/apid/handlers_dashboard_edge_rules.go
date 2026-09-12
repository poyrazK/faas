package main

// Dashboard surface for per-app edge rules (issue #1397 / G4). The page is
// deliberately a thin projection over the existing edge-rule and CORS-preset
// store/API paths. Browser forms are adapted to JSON and keep the API's
// validation, quota, ownership, and RFC7807 behavior intact.

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardEdgeRulesAction     = "app_edge_rules_mutation"
	dashboardEdgeRulesCSRFCookie = "faas_csrf_app_edge_rules"
)

// parseAppEdgeRulesPath recognizes the per-app edge-rules page.
func parseAppEdgeRulesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/edge-rules"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderAppEdgeRules(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	data := dashboard.AppEdgeRulesData{
		App:    dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain), IsPreview: app.PreviewOfSlug != ""},
		Action: dashboardEdgeRulesActionFlash(r),
	}
	if rules, listErr := s.store.ListEdgeRulesForApp(ctx, app.ID); listErr != nil {
		data.ErrorMessage = "Edge-rule data is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard edge rules: list rules", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
	} else {
		data.Rules = projectDashboardEdgeRules(rules)
		for _, rule := range rules {
			if isSecurityHeadersRule(rule) && rule.Enabled {
				data.SecurityHeadersEnabled = true
				break
			}
		}
	}
	if presets, listErr := s.store.ListCorsPresetsForAccount(ctx, acct.ID); listErr != nil {
		log.Warn("dashboard edge rules: list CORS presets", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
	} else {
		data.CorsPresets = projectDashboardCorsPresets(presets, app.ID)
	}
	if s.sessions != nil {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardEdgeRulesAction, acct.ID, dashboardEdgeRulesCSRFCookie)
		if tokenErr != nil {
			log.Warn("dashboard edge rules: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardEdgeRulesCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}

	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard edge rules: count apps", "account_id", acct.ID, "err", countErr)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title:   "Edge rules — " + app.Slug,
		Body:    "edge_rules",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectDashboardEdgeRules(rows []state.EdgeRule) []dashboard.EdgeRulePageItem {
	items := make([]dashboard.EdgeRulePageItem, 0, len(rows))
	for _, row := range rows {
		actionJSON, _ := json.Marshal(row.Action)
		item := dashboard.EdgeRulePageItem{
			ID: row.ID, Kind: string(row.Kind), MatchHost: row.MatchHost, MatchPath: row.MatchPath,
			MatchMethods: strings.Join(row.MatchMethods, ", "), Priority: row.Priority, Enabled: row.Enabled,
			ActionJSON: string(actionJSON), ActionSummary: edgeRuleActionSummary(row),
			CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt),
			SecurityPreset: isSecurityHeadersRule(row),
		}
		items = append(items, item)
	}
	return items
}

func projectDashboardCorsPresets(rows []state.CorsPreset, appID string) []dashboard.CorsPresetPageItem {
	items := make([]dashboard.CorsPresetPageItem, 0, len(rows))
	for _, row := range rows {
		if row.AppID != "" && row.AppID != appID {
			continue
		}
		scope := "Account-wide"
		if row.AppID != "" {
			scope = "This app"
		}
		items = append(items, dashboard.CorsPresetPageItem{
			ID: row.ID, Name: row.Name, Description: row.Description, Scope: scope,
			AllowOrigins: row.AllowOrigins, AllowMethods: row.AllowMethods, AllowHeaders: row.AllowHeaders,
			ExposeHeaders: row.ExposeHeaders, AllowCredentials: row.AllowCredentials, MaxAgeSeconds: row.MaxAgeSeconds,
			CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt),
		})
	}
	return items
}

func edgeRuleActionSummary(rule state.EdgeRule) string {
	switch rule.Kind {
	case state.EdgeRuleKindHeaders:
		if isSecurityHeadersRule(rule) {
			return "Security headers preset"
		}
		return "Request/response headers"
	case state.EdgeRuleKindCORSA:
		return "CORS policy"
	case state.EdgeRuleKindRedirect:
		return "Redirect"
	case state.EdgeRuleKindRewrite:
		return "Path rewrite"
	case state.EdgeRuleKindRoute:
		return "App route"
	default:
		return string(rule.Kind) + " rule"
	}
}

func dashboardEdgeRulesActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "created", "updated", "deleted", "security_headers_enabled":
		return r.URL.Query().Get("action")
	case "error":
		return "error"
	default:
		return ""
	}
}

func (s *server) dashboardCreateEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse edge rule form"))
		return
	}
	actionRaw := strings.TrimSpace(r.FormValue("action"))
	kind := strings.TrimSpace(r.FormValue("kind"))
	if kind == string(state.EdgeRuleKindRespond) {
		status := 200
		if rawStatus := strings.TrimSpace(r.FormValue("respond_status")); rawStatus != "" {
			parsed, err := strconv.Atoi(rawStatus)
			if err != nil {
				api.WriteProblem(w, api.ErrValidation("respond status must be an integer"))
				return
			}
			status = parsed
		}
		body := strings.TrimSpace(r.FormValue("respond_body"))
		action := api.EdgeRuleRespondAction{StatusCode: status}
		if body != "" {
			action.Body = json.RawMessage(body)
		}
		actionBytes, err := json.Marshal(action)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("could not encode respond action"))
			return
		}
		actionRaw = string(actionBytes)
	}
	if actionRaw == "" || !json.Valid([]byte(actionRaw)) {
		api.WriteProblem(w, api.ErrValidation("action must be valid JSON"))
		return
	}
	req := api.CreateEdgeRuleRequest{
		MatchHost: strings.TrimSpace(r.FormValue("match_host")), MatchPath: strings.TrimSpace(r.FormValue("match_path")),
		MatchMethods: splitDashboardEdgeRuleMethods(r.FormValue("match_methods")), Kind: kind,
		Action: json.RawMessage(actionRaw),
	}
	enabled := r.FormValue("enabled") != ""
	req.Enabled = &enabled
	if rawPriority := strings.TrimSpace(r.FormValue("priority")); rawPriority != "" {
		priority, err := strconv.Atoi(rawPriority)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("priority must be an integer"))
			return
		}
		req.Priority = &priority
	}
	slug := r.PathValue("slug")
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/edge-rules", "", req, s.createEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=created", http.StatusSeeOther)
}

func (s *server) dashboardToggleEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rule, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil || rule.AccountID != acct.ID || rule.AppID != app.ID {
		http.NotFound(w, r)
		return
	}
	enabled := !rule.Enabled
	req := api.UpdateEdgeRuleRequest{Enabled: &enabled}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), id, req, s.updateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=updated", http.StatusSeeOther)
}

func (s *server) dashboardDeleteEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodDelete, "/v1/edge-rules/"+url.PathEscape(id), id, nil, s.deleteEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=deleted", http.StatusSeeOther)
}

func (s *server) dashboardSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug := r.PathValue("slug")
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rules, err := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect edge rules"))
		return
	}
	for _, rule := range rules {
		if isSecurityHeadersRule(rule) {
			if !rule.Enabled {
				enabled := true
				resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(rule.ID), rule.ID, api.UpdateEdgeRuleRequest{Enabled: &enabled}, s.updateEdgeRule)
				if !dashboardMutationSucceeded(w, resp) {
					return
				}
			}
			http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=security_headers_enabled", http.StatusSeeOther)
			return
		}
	}
	host := app.Slug
	if parsed, parseErr := url.Parse(appURLForDomain(app.Slug, s.domain)); parseErr == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	enabled := true
	priority := 1000
	req := api.CreateEdgeRuleRequest{
		MatchHost: host, MatchPath: "/*", Priority: &priority, Enabled: &enabled,
		Kind: string(state.EdgeRuleKindHeaders), Action: securityHeadersActionJSON(),
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/edge-rules", "", req, s.createEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=security_headers_enabled", http.StatusSeeOther)
}

func securityHeadersActionJSON() json.RawMessage {
	action := api.EdgeRuleHeadersAction{ResponseHeaders: []api.EdgeRuleHeaderOp{
		{Name: "Strict-Transport-Security", Value: "max-age=31536000; includeSubDomains", Action: "set"},
		{Name: "X-Content-Type-Options", Value: "nosniff", Action: "set"},
		{Name: "Referrer-Policy", Value: "strict-origin-when-cross-origin", Action: "set"},
		{Name: "Content-Security-Policy", Value: "frame-ancestors 'none'", Action: "set"},
	}}
	raw, _ := json.Marshal(action)
	return raw
}

func isSecurityHeadersRule(rule state.EdgeRule) bool {
	if rule.Kind != state.EdgeRuleKindHeaders || (rule.MatchPath != "/" && rule.MatchPath != "/*") || rule.Action.Headers == nil || len(rule.Action.Headers.RequestHeaders) != 0 {
		return false
	}
	want := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "frame-ancestors 'none'",
	}
	if len(rule.Action.Headers.ResponseHeaders) != len(want) {
		return false
	}
	for _, op := range rule.Action.Headers.ResponseHeaders {
		if op.Action != "set" || want[op.Name] != op.Value {
			return false
		}
		delete(want, op.Name)
	}
	return len(want) == 0
}

func splitDashboardEdgeRuleMethods(raw string) []string {
	parts := strings.Split(raw, ",")
	methods := make([]string, 0, len(parts))
	for _, part := range parts {
		if method := strings.ToUpper(strings.TrimSpace(part)); method != "" {
			methods = append(methods, method)
		}
	}
	return methods
}

func (s *server) verifyDashboardEdgeRulesCSRF(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardEdgeRulesAction, accountID, dashboardEdgeRulesCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

func (s *server) forwardDashboardEdgeRuleJSON(r *http.Request, acct state.Account, method, path, id string, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method = method
	req.URL = cloneDashboardURL(r.URL, path, "")
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", r.PathValue("slug"))
	req.SetPathValue("id", id)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}
