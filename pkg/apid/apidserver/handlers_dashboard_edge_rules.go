package apidserver

// Dashboard surface for per-app edge rules (issue #1397 / G4). The page is
// deliberately a thin projection over the existing edge-rule and CORS-preset
// store/API paths. Browser forms are adapted to JSON and keep the API's
// validation, quota, ownership, and RFC7807 behavior intact.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
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

func (s *server) renderAppEdgeRules(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string, traceForm *dashboard.EdgeRuleTraceFormData, traceInput *edgeruletrace.Input) {
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
	data.Protection = s.edgeRules.AppEdgeProtection(ctx, app, edgerules.EdgeProtectionDefaultRange)
	data.AttackModeAvailable = s.edgeRules.AttackModeReleased(acct)
	data.Trace = dashboard.EdgeRuleTraceFormData{Host: app.Slug, Path: "/", Method: http.MethodGet}
	if appURL, parseErr := url.Parse(data.App.URL); parseErr == nil && appURL.Hostname() != "" {
		data.Trace.Host = appURL.Hostname()
	}
	presets, presetErr := s.store.ListCorsPresetsForAccount(ctx, acct.ID)
	if presetErr != nil {
		log.Warn("dashboard edge rules: list CORS presets", "account_id", acct.ID, "app_id", app.ID, "err", presetErr)
	} else {
		data.CorsPresets = projectDashboardCorsPresets(presets, app.ID)
	}
	if traceForm != nil {
		data.Trace = *traceForm
		data.Trace.Headers = edgeruletrace.RedactHeaderInputForDisplay(data.Trace.Headers)
	}
	if rules, listErr := s.store.ListEdgeRulesForApp(ctx, app.ID); listErr != nil {
		data.ErrorMessage = "Edge-rule data is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard edge rules: list rules", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
	} else {
		data.Rules = projectDashboardEdgeRules(rules, func(row state.EdgeRule) []string { return s.edgeRules.RuleResponse(acct, row).UnreleasedFeatures })
		data.Recipes = dashboardEdgeRuleRecipes(rules, acct.Plan, func(feature string) bool { return s.edgeRules.FeatureReleased(acct, feature) })
		if limits, ok := api.LimitsFor(acct.Plan); ok && limits.AlertRuleLimitPerApp > 0 {
			data.RecipeAlerts = true
		}
		data.Suggestions = s.dashboardEdgeRuleSuggestions(ctx, log, acct, app, data.Recipes)
		data.Hosts = s.dashboardEdgeRuleHostCoverage(ctx, log, app, rules)
		if hits, ok := s.addDashboardEdgeRuleHits(ctx, log, app.ID, data.Rules); ok {
			data.Review = dashboardEdgeRuleReview(rules, data.Rules, hits)
		}
		data.Events = s.loadDashboardEdgeRuleEvents(r, log, acct, app, rules)
		data.WAFReview = s.loadDashboardEdgeWAFReview(r, log, acct, app, rules)
		for _, rule := range rules {
			if isSecurityHeadersRule(rule) && rule.Enabled {
				data.SecurityHeadersEnabled = true
				break
			}
		}
		if traceInput != nil {
			traceContext := *traceInput
			traceContext.AppMaintenanceLoaded = true
			traceContext.AppMaintenanceMode = app.MaintenanceMode
			effectiveLimits := appEffectiveLimits(app, acct.Plan)
			traceContext.AppRequestBudgetLoaded = effectiveLimits.RequestBudgetMS > 0 && effectiveLimits.RequestBudgetMaxMS > 0
			traceContext.RequestBudgetMS = effectiveLimits.RequestBudgetMS
			traceContext.RequestBudgetMaxMS = effectiveLimits.RequestBudgetMaxMS
			traceContext.RequestTimeoutS = app.Manifest.RequestTimeoutS
			traceContext.AsyncWorkloadContextLoaded = true
			traceContext.AsyncRequestInvocationsEnabled = app.AcceptsRequestInvocations()
			traceContext.AsyncAppRetryPolicyLoaded = true
			traceContext.AsyncAppRetryPolicy = retryPolicyDTOFromJSON(app.RetryPolicyJSON)
			traceContext.AppThrottleContextLoaded = effectiveLimits.AppRequestRateRPS > 0 && effectiveLimits.AppRequestBurst > 0
			traceContext.AppRequestRateRPS = effectiveLimits.AppRequestRateRPS
			traceContext.AppRequestRateBurst = effectiveLimits.AppRequestBurst
			traceContext.AccountRequestRateRPM = effectiveLimits.AccountRequestRateRPM
			if planLimits, ok := api.LimitsFor(acct.Plan); ok {
				traceContext.ThrottlePlanLimitsLoaded = planLimits.RateLimitRPS > 0 && planLimits.RateLimitBurst > 0
				traceContext.ThrottlePlanMaxRPS = planLimits.RateLimitRPS
				traceContext.ThrottlePlanMaxBurst = planLimits.RateLimitBurst
				traceContext.AsyncPlanLimitsLoaded = true
				traceContext.AsyncPlan = acct.Plan
				traceContext.AsyncInvokeAllowed = planLimits.AsyncInvokeAllowed
				traceContext.AsyncMaxPayloadBytes = planLimits.MaxSourceBytesPerInvocation
				traceContext.AsyncMaxQueueAttempts = planLimits.MaxQueueAttempts
				traceContext.AsyncMaxDeadlineSeconds = planLimits.MaxAsyncInvocationDeadlineSeconds
			}
			traceContext.OnlyAllowDeclaredRoutes = app.OnlyAllowDeclaredRoutes
			traceContext.DeclaredRoutes = make([]api.DeclaredRoute, 0, len(app.DeclaredRoutes))
			for _, route := range app.DeclaredRoutes {
				traceContext.DeclaredRoutes = append(traceContext.DeclaredRoutes, api.DeclaredRoute{Path: route.Path, Methods: append([]string(nil), route.Methods...)})
			}
			if traceContext.OnlyAllowDeclaredRoutes && len(traceContext.DeclaredRoutes) == 0 {
				doc, _, docErr := s.store.GetAppOpenAPIDoc(ctx, app.ID, acct.ID)
				switch {
				case docErr == nil:
					traceContext.DeclaredRouteDocumentLoaded = true
					traceContext.DeclaredRouteOpenAPIDoc = append([]byte(nil), doc...)
				case errors.Is(docErr, state.ErrNotFound):
					traceContext.DeclaredRouteDocumentMissing = true
				default:
					log.Warn("dashboard edge rules: load declared-route OpenAPI document", "account_id", acct.ID, "app_id", app.ID, "err", docErr)
				}
			}
			traceContext.AppCORSDefaultsLoaded = true
			traceContext.CORSDefaultEnabled = app.CORSDefaultEnabled
			traceContext.CORSDefaultOrigins = append([]string(nil), app.CORSDefaultOrigins...)
			if presetErr == nil {
				traceContext.CorsPresets = corsPresetResponsesFromRows(presets)
			}
			apiRules := make([]api.EdgeRuleResponse, 0, len(rules))
			for _, rule := range rules {
				apiRules = append(apiRules, s.edgeRules.RuleResponse(acct, rule))
			}
			if names := edgeruletrace.ReferencedEdgeRuleLists(apiRules); len(names) > 0 {
				if store, ok := s.store.(state.EdgeRuleListStore); ok {
					lists, listErr := store.EdgeRuleListsByName(ctx, acct.ID, names)
					if listErr != nil {
						log.Warn("dashboard edge rules: load edge rule lists", "account_id", acct.ID, "err", listErr)
					}
					for _, l := range lists {
						traceContext.EdgeRuleLists = append(traceContext.EdgeRuleLists, edgerules.EdgeRuleListResponse(l, nil, true))
					}
				}
			}
			result, traceErr := edgeruletrace.Simulate(traceContext, apiRules)
			if traceErr != nil {
				data.Trace.ErrorMessage = "The request could not be simulated. Check the request fields and try again."
			} else {
				data.Trace.Result = &result
			}
		} else if data.Trace.Submitted && data.Trace.ErrorMessage == "" && data.ErrorMessage != "" {
			data.Trace.ErrorMessage = "Edge rules are temporarily unavailable, so this request could not be traced."
		}
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

// dashboardTraceEdgeRules simulates a submitted request against the current
// app's stored edge-rule snapshot. It is a read-only, account-scoped form
// post; it neither contacts the gateway nor stores request data.
func (s *server) dashboardTraceEdgeRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	// VerifyAuthenticatedNamed reads a form value, so apply the body bound
	// before CSRF validation as well as before the handler parses fields.
	// Form encoding can expand the bounded UTF-8 body to roughly 3x its
	// original byte length; cap the complete POST before CSRF parsing.
	requestLimit := int64(edgeruletrace.MaxTraceBodyBytes*4 + 64*1024)
	r.Body = http.MaxBytesReader(w, r.Body, requestLimit)
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	form := dashboard.EdgeRuleTraceFormData{Submitted: true}
	if err := r.ParseForm(); err != nil {
		form.ErrorMessage = fmt.Sprintf("The trace form could not be read. The simulated request body is limited to %d bytes.", edgeruletrace.MaxTraceBodyBytes)
		s.renderAppEdgeRules(w, r, s.log, acct, r.PathValue("slug"), &form, nil)
		return
	}
	form.Host = strings.TrimSpace(r.FormValue("trace_host"))
	form.Path = r.FormValue("trace_path")
	form.Method = r.FormValue("trace_method")
	form.Headers = r.FormValue("trace_headers")
	rawBody := r.FormValue("trace_body")
	form.BodyProvided = r.FormValue("trace_body_provided") != "" || rawBody != ""
	form.ClientIP = r.FormValue("trace_client_ip")
	form.Country = r.FormValue("trace_country")
	lines := strings.Split(strings.ReplaceAll(form.Headers, "\r\n", "\n"), "\n")
	headerLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) != "" {
			headerLines = append(headerLines, line)
		}
	}
	headers, err := edgeruletrace.ParseRequestHeaders(headerLines)
	if err != nil {
		form.ErrorMessage = "Headers must be valid Name: Value pairs, one per line."
		s.renderAppEdgeRules(w, r, s.log, acct, r.PathValue("slug"), &form, nil)
		return
	}
	// A path typed with "?query" feeds validate-rule query schemas.
	tracePath, traceQuery, _ := strings.Cut(form.Path, "?")
	input, err := edgeruletrace.NormalizeInput(edgeruletrace.Input{
		App: r.PathValue("slug"), Host: form.Host, Path: tracePath, Query: traceQuery, Method: form.Method,
		ClientIP: form.ClientIP, Country: form.Country, Headers: headers,
		Body: []byte(rawBody), BodyProvided: form.BodyProvided,
		RequestBodyMaxBytes: acct.Plan.MaxRequestBodyBytes(),
	})
	if err != nil {
		form.ErrorMessage = err.Error()
		s.renderAppEdgeRules(w, r, s.log, acct, r.PathValue("slug"), &form, nil)
		return
	}
	form.Host, form.Path, form.Method = input.Host, input.Path, input.Method
	if traceQuery != "" {
		form.Path += "?" + traceQuery
	}
	form.ClientIP, form.Country = input.ClientIP, input.Country
	s.renderAppEdgeRules(w, r, s.log, acct, r.PathValue("slug"), &form, &input)
}

// unreleased names the gated features a rule uses that the account's flags
// do not release (ADR-20261023113721).
func projectDashboardEdgeRules(rows []state.EdgeRule, unreleased func(state.EdgeRule) []string) []dashboard.EdgeRulePageItem {
	items := make([]dashboard.EdgeRulePageItem, 0, len(rows))
	for _, row := range rows {
		actionJSON, _ := json.Marshal(row.Action)
		item := dashboard.EdgeRulePageItem{
			ID: row.ID, Kind: string(row.Kind), MatchHost: row.MatchHost, MatchPath: row.MatchPath,
			MatchMethods: strings.Join(row.MatchMethods, ", "), Priority: row.Priority, Enabled: row.Enabled,
			ActionJSON: string(actionJSON), ActionSummary: edgeRuleActionSummary(row),
			CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt),
			SecurityPreset: isSecurityHeadersRule(row),
			Name:           row.Name, Mode: edgeRuleModeOrDefault(row.Mode),
			AppWide:    api.IsEdgeRuleAppHosts(row.MatchHost),
			AttackMode: row.AttackMode(),
		}
		for _, feature := range unreleased(row) {
			item.Unreleased = append(item.Unreleased, dashboardEdgeFeatureLabel(feature))
		}
		items = append(items, item)
	}
	return items
}

// dashboardEdgeFeatureLabel names an unreleased edge feature for the rule
// list.
func dashboardEdgeFeatureLabel(feature string) string {
	switch feature {
	case api.EdgeFeatureChallenge:
		return "challenge rules"
	case api.EdgeFeatureWAFBlocking:
		return "WAF warn and block"
	case api.EdgeFeaturePenaltyBox:
		return "penalties"
	case api.EdgeFeatureAICrawlers:
		return "AI crawler conditions"
	case api.EdgeFeatureAttackMode:
		return "attack mode"
	}
	return feature
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
	case "created", "updated", "deleted", "security_headers_enabled", "mode_enforce", "mode_log", "recipe_log", "recipe_enforce",
		"recipe_log_alert", "recipe_enforce_alert", "recipe_alert_failed", "attack_mode_on", "attack_mode_off", "waf_excluded":
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
		writeDashboardUnauthorized(w, r)
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
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/edge-rules", "", req, s.edgeRules.CreateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=created", http.StatusSeeOther)
}

func (s *server) dashboardToggleEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
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
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), id, req, s.edgeRules.UpdateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=updated", http.StatusSeeOther)
}

func (s *server) dashboardDeleteEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodDelete, "/v1/edge-rules/"+url.PathEscape(id), id, nil, s.edgeRules.DeleteEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=deleted", http.StatusSeeOther)
}

func (s *server) dashboardSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
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
				resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(rule.ID), rule.ID, api.UpdateEdgeRuleRequest{Enabled: &enabled}, s.edgeRules.UpdateEdgeRule)
				if !dashboardMutationSucceeded(w, resp) {
					return
				}
			}
			http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action=security_headers_enabled", http.StatusSeeOther)
			return
		}
	}
	enabled := true
	priority := 1000
	req := api.CreateEdgeRuleRequest{
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/*", Priority: &priority, Enabled: &enabled,
		Kind: string(state.EdgeRuleKindHeaders), Action: securityHeadersActionJSON(),
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/edge-rules", "", req, s.edgeRules.CreateEdgeRule)
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

// dashboardApplyEdgeRuleRecipe adds one protection recipe's rule (ADR-986)
// through the JSON create handler, so validation, plan gates and quotas are
// the API's. The rule starts in log mode unless the form asks to enforce.
func (s *server) dashboardApplyEdgeRuleRecipe(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse recipe form"))
		return
	}
	slug := r.PathValue("slug")
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	recipe, ok := api.EdgeRuleRecipeByID(r.PostFormValue("recipe"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	values := map[string]string{}
	for _, p := range recipe.Params {
		if v := strings.TrimSpace(r.PostFormValue(p.Name)); v != "" {
			values[p.Name] = v
		}
	}
	enforce := r.PostFormValue("enforce") != ""
	req, prob := api.BuildEdgeRuleRecipe(recipe.ID, api.EdgeRuleAppHosts, values, enforce)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	alert, prob := dashboardRecipeAlertInput(r, acct.Plan)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/edge-rules", "", req, s.edgeRules.CreateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	action := "recipe_log"
	if enforce {
		action = "recipe_enforce"
	}
	if alert != nil {
		action = s.addDashboardRecipeAlert(r, acct, slug, recipe.ID, resp, alert, action)
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action="+action, http.StatusSeeOther)
}

// dashboardEdgeRuleRecipes projects the recipe catalog for the page: which
// recipes already have a rule, and which need a higher plan.
// released reports whether the account may use an edge feature
// (api.EdgeFeature*, ADR-20261023113721): a recipe for an unreleased kind
// says so instead of offering a form, and an unreleased penalty field is
// left out.
func dashboardEdgeRuleRecipes(rules []state.EdgeRule, plan api.Plan, released func(feature string) bool) []dashboard.EdgeRuleRecipeView {
	limits, _ := api.LimitsFor(plan)
	out := make([]dashboard.EdgeRuleRecipeView, 0, len(api.EdgeRuleRecipes))
	for _, recipe := range api.EdgeRuleRecipes {
		view := dashboard.EdgeRuleRecipeView{ID: recipe.ID, Title: recipe.Title, Summary: recipe.Summary, LogNote: recipe.LogNote}
		for _, p := range recipe.Params {
			if p.Name == api.EdgeRuleRecipePenaltyParamName && !released(api.EdgeFeaturePenaltyBox) {
				continue
			}
			view.Params = append(view.Params, dashboard.EdgeRuleRecipeParamView{Name: p.Name, Label: p.Label, Help: p.Help, Default: p.Default, Placeholder: p.Placeholder, Required: p.Required})
		}
		description := api.EdgeRuleRecipeDescription(recipe.ID)
		for _, rule := range rules {
			if rule.Description == description {
				view.Added = true
				break
			}
		}
		if recipe.Kind == string(state.EdgeRuleKindIP) && !limits.EdgeRulesIPAllowed {
			view.Blocked = "Available on the Hobby plan and above, which include IP edge rules."
		}
		if recipe.Kind == string(state.EdgeRuleKindChallenge) && limits.EdgeRulesChallengePerApp == 0 {
			view.Blocked = "Available on the Hobby plan and above, which include challenge rules."
		}
		if recipe.Kind == string(state.EdgeRuleKindChallenge) && view.Blocked == "" && !released(api.EdgeFeatureChallenge) {
			view.Blocked = "Challenge rules are not yet available to this account."
		}
		if recipe.ID == api.EdgeRuleRecipeControlAICrawlers && view.Blocked == "" && !released(api.EdgeFeatureAICrawlers) {
			view.Blocked = "AI crawler conditions are not yet available to this account."
		}
		out = append(out, view)
	}
	return out
}

// dashboardSelectorHostCoverage lists the <app>--<name> hostnames the edge
// routes to the app's companions and public listeners, so the page shows that
// they are public and which rules cover them.
func (s *server) dashboardSelectorHostCoverage(ctx context.Context, log *slog.Logger, app state.App, platformHost string) []dashboard.EdgeRuleHostCoverage {
	live, err := s.store.LiveDeployments(ctx, app.ID)
	if err != nil {
		log.Warn("dashboard edge rules: list live deployments", "app_id", app.ID, "err", err)
	}
	var hosts []dashboard.EdgeRuleHostCoverage
	for _, selector := range edgerules.SelectorHosts(platformHost, live, app.Manifest.Ports) {
		hosts = append(hosts, dashboard.EdgeRuleHostCoverage{Host: selector.Host, Selector: selector.Name, PublicPort: selector.PublicPort})
	}
	return hosts
}

// dashboardEdgeRuleHostCoverage counts, for each hostname the app serves
// (its platform host, its companion and public-listener selector hosts, and
// verified custom domains), the enabled rules that
// apply to it: host-specific, pattern ("*", "*.example.com") and app-wide
// ("@app", ADR-1191) rules alike.
func (s *server) dashboardEdgeRuleHostCoverage(ctx context.Context, log *slog.Logger, app state.App, rules []state.EdgeRule) []dashboard.EdgeRuleHostCoverage {
	hosts := []dashboard.EdgeRuleHostCoverage{}
	if parsed, err := url.Parse(appURLForDomain(app.Slug, s.domain)); err == nil && parsed.Hostname() != "" {
		hosts = append(hosts, dashboard.EdgeRuleHostCoverage{Host: parsed.Hostname()})
		hosts = append(hosts, s.dashboardSelectorHostCoverage(ctx, log, app, parsed.Hostname())...)
	}
	domains, err := s.store.ListDomainsForApp(ctx, app.ID)
	if err != nil {
		log.Warn("dashboard edge rules: list custom domains", "app_id", app.ID, "err", err)
	}
	for _, d := range domains {
		if !d.VerifiedAt.IsZero() {
			hosts = append(hosts, dashboard.EdgeRuleHostCoverage{Host: strings.ToLower(d.Domain), Custom: true})
		}
	}
	for i := range hosts {
		for _, rule := range rules {
			if rule.Enabled && api.EdgeRuleHostMatches(rule.MatchHost, hosts[i].Host) {
				hosts[i].Rules++
			}
		}
	}
	return hosts
}

// recipeAlertInput is the optional "alert me" part of a recipe form.
type recipeAlertInput struct {
	perHour int
	url     string
	secret  string
}

// dashboardRecipeAlertInput reads and checks the recipe form's alert fields
// before any rule is created, so a bad threshold, missing webhook or plan
// without alerts never leaves a half-added recipe. nil means no alert.
func dashboardRecipeAlertInput(r *http.Request, plan api.Plan) (*recipeAlertInput, *api.Problem) {
	if r.PostFormValue("alert") == "" {
		return nil, nil
	}
	if limits, ok := api.LimitsFor(plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		return nil, api.ErrPlanAlertRulesNotAllowed(plan)
	}
	in := &recipeAlertInput{perHour: api.EdgeRuleRecipeAlertDefaultPerHour, url: strings.TrimSpace(r.PostFormValue("webhook_url")), secret: r.PostFormValue("webhook_secret")}
	if raw := strings.TrimSpace(r.PostFormValue("alert_threshold")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, api.ErrValidation("alert threshold must be a whole number of matches per hour")
		}
		in.perHour = n
	}
	if _, prob := api.EdgeRuleRecipeAlert(api.EdgeRuleRecipeBlockUnverifiedBots, "pending", in.perHour, in.url, in.secret); prob != nil {
		return nil, prob
	}
	return in, nil
}

// addDashboardRecipeAlert creates the recipe's "alert me" rule for the edge
// rule in created through the JSON alert handler (validation, egress check,
// quota and secret sealing stay the API's) and returns the flash action. A
// failure keeps the rule and says the alert was not added.
func (s *server) addDashboardRecipeAlert(r *http.Request, acct state.Account, slug, recipeID string, created *httptest.ResponseRecorder, in *recipeAlertInput, action string) string {
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(created.Body.Bytes(), &rule); err != nil || rule.ID == "" {
		return "recipe_alert_failed"
	}
	req, prob := api.EdgeRuleRecipeAlert(recipeID, rule.ID, in.perHour, in.url, in.secret)
	if prob != nil {
		return "recipe_alert_failed"
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/alerts", "", req, s.createAlertRule)
	if resp.Code < http.StatusOK || resp.Code >= http.StatusMultipleChoices {
		return "recipe_alert_failed"
	}
	return action + "_alert"
}

// dashboardEdgeRuleSuggestions turns the app's recipe suggestions (ADR-20261020105000)
// into forms for the recipes route: suggested parameters ride as hidden
// fields and any required parameter the suggestion cannot fill becomes an
// input. A plan without request telemetry, or a telemetry error, hides the
// section rather than failing the page.
func (s *server) dashboardEdgeRuleSuggestions(ctx context.Context, log *slog.Logger, acct state.Account, app state.App, recipes []dashboard.EdgeRuleRecipeView) []dashboard.EdgeRuleSuggestionView {
	days := api.EdgeRuleRecipeSuggestionDays(acct.Plan)
	if days == 0 {
		return nil
	}
	suggestions, err := s.edgeRules.RecipeSuggestions(ctx, acct, app, days)
	if err != nil {
		log.Warn("dashboard edge rules: recipe suggestions", "app_id", app.ID, "err", err)
		return nil
	}
	byID := map[string]dashboard.EdgeRuleRecipeView{}
	for _, r := range recipes {
		byID[r.ID] = r
	}
	out := make([]dashboard.EdgeRuleSuggestionView, 0, len(suggestions.Suggestions))
	for _, sg := range suggestions.Suggestions {
		view := dashboardSuggestionView(byID[sg.Recipe], sg.Title, sg.Reason, sg.Params)
		if alt := sg.Alternative; alt != nil {
			altView := dashboardSuggestionView(byID[alt.Recipe], alt.Title, "", alt.Params)
			view.Alternative = &altView
		}
		out = append(out, view)
	}
	return out
}

// dashboardSuggestionView is one recipe form: suggested parameters ride as
// hidden fields, required ones the suggestion cannot fill become inputs.
func dashboardSuggestionView(recipe dashboard.EdgeRuleRecipeView, title, reason string, params map[string]string) dashboard.EdgeRuleSuggestionView {
	view := dashboard.EdgeRuleSuggestionView{Recipe: recipe.ID, Title: title, Reason: reason, Blocked: recipe.Blocked}
	for _, p := range recipe.Params {
		if v, ok := params[p.Name]; ok {
			view.Hidden = append(view.Hidden, dashboard.EdgeRuleSuggestionField{Name: p.Name, Value: v})
		} else if p.Required {
			view.Inputs = append(view.Inputs, p)
		}
	}
	return view
}
