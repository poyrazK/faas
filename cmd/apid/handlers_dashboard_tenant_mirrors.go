package main

// Dashboard surfaces for tenant surfaces and traffic mirrors (issue #1397 /
// G9). The pages keep their read models deliberately small and use the
// existing JSON handlers for every mutation, preserving API validation,
// ownership checks, plan gates, auditing, and RFC 7807 responses.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardTenantSurfacesAction     = "app_tenant_surfaces_mutation"
	dashboardTenantSurfacesCSRFCookie = "faas_csrf_app_tenant_surfaces"
	dashboardMirrorsAction            = "app_mirrors_mutation"
	dashboardMirrorsCSRFCookie        = "faas_csrf_app_mirrors"
)

func parseAppTenantSurfacesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/tenant-surfaces"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func parseAppMirrorsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/mirrors"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderAppTenantSurfaces(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	limits, planKnown := api.LimitsFor(acct.Plan)
	data := dashboard.TenantSurfacesData{
		App:            dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		PlanAllowed:    planKnown && limits.TenantSurfacesAllowed,
		FeatureEnabled: s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()),
		Action:         tenantSurfacesActionFlash(r),
	}
	if data.FeatureEnabled && data.PlanAllowed {
		rows, listErr := s.store.ListTenantSurfacesForApp(ctx, app.ID)
		if listErr != nil {
			data.ErrorMessage = "Tenant-surface data is temporarily unavailable. Please try again shortly."
			log.Warn("dashboard tenant surfaces: list surfaces", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
		} else {
			data.Surfaces = s.projectDashboardTenantSurfaces(ctx, rows)
		}
	}
	if s.sessions != nil {
		if token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardTenantSurfacesAction, acct.ID, dashboardTenantSurfacesCSRFCookie); tokenErr != nil {
			log.Warn("dashboard tenant surfaces: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardTenantSurfacesCSRFCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}
	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard tenant surfaces: count apps", "account_id", acct.ID, "err", countErr)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{Title: "Tenant surfaces — " + app.Slug, Body: "tenant_surfaces", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) projectDashboardTenantSurfaces(ctx context.Context, rows []state.TenantSurface) []dashboard.TenantSurfacePageItem {
	items := make([]dashboard.TenantSurfacePageItem, 0, len(rows))
	for _, row := range rows {
		if row.Status == state.SurfaceStatusDeleted {
			continue
		}
		item := dashboard.TenantSurfacePageItem{
			ID: row.ID, Name: row.Name, CertKind: string(row.CertKind), Status: string(row.Status), CertState: string(row.CertState),
			CertLastError: row.CertLastError, CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt),
		}
		if !row.CertNotAfter.IsZero() {
			item.CertNotAfter = dashboardJobsTime(row.CertNotAfter)
		}
		hostnames, err := s.store.ListTenantHostnamesForSurface(ctx, row.ID)
		if err != nil {
			continue
		}
		item.Hostnames = make([]dashboard.TenantHostnamePageItem, 0, len(hostnames))
		for _, hostname := range hostnames {
			h := dashboard.TenantHostnamePageItem{Hostname: hostname.Hostname, Verified: hostname.Verified(), LastError: hostname.LastError, TXTRecord: "_faas-verify." + hostname.Hostname}
			if !hostname.VerifiedAt.IsZero() {
				h.VerifiedAt = dashboardJobsTime(hostname.VerifiedAt)
			}
			item.Hostnames = append(item.Hostnames, h)
		}
		items = append(items, item)
	}
	return items
}

func tenantSurfacesActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "created", "deleted", "hostname-added", "hostname-deleted":
		return r.URL.Query().Get("action")
	case "error":
		return "error"
	default:
		return ""
	}
}

func (s *server) renderAppMirrors(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	limits, planKnown := api.LimitsFor(acct.Plan)
	data := dashboard.MirrorsData{
		App:         dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		PlanAllowed: planKnown && limits.MirrorRuleAllowed,
		Action:      mirrorsActionFlash(r),
	}
	if data.PlanAllowed {
		rows, listErr := s.store.ListMirrorRules(ctx, app.ID)
		if listErr != nil {
			data.ErrorMessage = "Mirror data is temporarily unavailable. Please try again shortly."
			log.Warn("dashboard mirrors: list rules", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
		} else {
			data.Rules = make([]dashboard.MirrorPageItem, 0, len(rows))
			since := timeNow().Add(-time.Hour)
			for _, row := range rows {
				if row.AccountID != acct.ID || row.AppID != app.ID {
					continue
				}
				item := dashboard.MirrorPageItem{ID: row.ID, SourceDeploymentID: row.SourceDeploymentID, MirrorDeploymentID: row.MirrorDeploymentID, Percent: row.Percent, Enabled: row.Enabled, IncludeBody: row.IncludeBody, RedactHeaders: append([]string(nil), row.RedactHeaders...), AlwaysStrippedHeaders: append([]string(nil), api.MirrorAlwaysStrippedHeaders...), CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt)}
				summary, summaryErr := s.store.MirrorSummary(ctx, row.ID, since)
				if summaryErr != nil {
					data.ErrorMessage = "Some mirror summary counters are temporarily unavailable."
					log.Warn("dashboard mirrors: summary", "account_id", acct.ID, "app_id", app.ID, "rule_id", row.ID, "err", summaryErr)
				} else {
					item.Summary = dashboard.MirrorSummaryPageItem{TotalInvocations: int64(summary.TotalInvocations), StatusDiffCount: int64(summary.StatusDiffCount), SchemaDiffCount: int64(summary.SchemaDiffCount), BodyDiffCount: int64(summary.BodyDiffCount), MeanLatencyDiffMs: int64(summary.MeanLatencyDiffMs), P99LatencyDiffMs: int64(summary.P99LatencyDiffMs), CrashCount: int64(summary.CrashCount), WindowLabel: "last 1h"}
				}
				data.Rules = append(data.Rules, item)
			}
		}
	}
	if s.sessions != nil {
		if token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardMirrorsAction, acct.ID, dashboardMirrorsCSRFCookie); tokenErr != nil {
			log.Warn("dashboard mirrors: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardMirrorsCSRFCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}
	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard mirrors: count apps", "account_id", acct.ID, "err", countErr)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{Title: "Mirrors — " + app.Slug, Body: "mirrors", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func mirrorsActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "created", "toggled", "deleted":
		return r.URL.Query().Get("action")
	case "error":
		return "error"
	default:
		return ""
	}
}

func (s *server) verifyDashboardTenantSurfacesCSRF(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardTenantSurfacesAction, accountID, dashboardTenantSurfacesCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

func (s *server) verifyDashboardMirrorsCSRF(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMirrorsAction, accountID, dashboardMirrorsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

func (s *server) dashboardCreateTenantSurface(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardTenantSurfacesCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse tenant surface form"))
		return
	}
	hostnames := splitDashboardValues(r.FormValue("hostnames"))
	req := api.CreateTenantSurfaceRequest{AppID: "", Name: strings.TrimSpace(r.FormValue("name")), CertKind: strings.TrimSpace(r.FormValue("cert_kind")), Hostnames: hostnames}
	slug := r.PathValue("slug")
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	req.AppID = app.ID
	resp := s.forwardDashboardTenantJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/tenant-surfaces", "", "", req, s.createTenantSurface)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/tenant-surfaces?action=created", http.StatusSeeOther)
}

func (s *server) dashboardDeleteTenantSurface(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardTenantSurfacesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardTenantJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/tenant-surfaces/"+url.PathEscape(id), id, "", nil, s.deleteTenantSurface)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/tenant-surfaces?action=deleted", http.StatusSeeOther)
}

func (s *server) dashboardAddTenantHostname(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardTenantSurfacesCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse hostname form"))
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	req := api.AddTenantHostnameRequest{Hostname: strings.TrimSpace(r.FormValue("hostname"))}
	resp := s.forwardDashboardTenantJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/tenant-surfaces/"+url.PathEscape(id)+"/hostnames", id, "", req, s.addTenantHostname)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/tenant-surfaces?action=hostname-added", http.StatusSeeOther)
}

func (s *server) dashboardRemoveTenantHostname(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardTenantSurfacesCSRF(w, r, acct.ID) {
		return
	}
	slug, id, hostname := r.PathValue("slug"), r.PathValue("id"), r.PathValue("hostname")
	path := "/v1/apps/" + url.PathEscape(slug) + "/tenant-surfaces/" + url.PathEscape(id) + "/hostnames/" + url.PathEscape(hostname)
	resp := s.forwardDashboardTenantJSON(r, acct, http.MethodDelete, path, id, hostname, nil, s.removeTenantHostname)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/tenant-surfaces?action=hostname-deleted", http.StatusSeeOther)
}

func (s *server) dashboardCreateMirrorRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardMirrorsCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse mirror form"))
		return
	}
	percent, err := strconv.Atoi(strings.TrimSpace(r.FormValue("percent")))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid percent", "percent must be an integer"))
		return
	}
	req := api.CreateMirrorRuleRequest{SourceDeploymentID: strings.TrimSpace(r.FormValue("source_deployment_id")), MirrorDeploymentID: strings.TrimSpace(r.FormValue("mirror_deployment_id")), Percent: percent, IncludeBody: dashboardCheckbox(r.FormValue("include_body")), RedactHeaders: splitDashboardValues(r.FormValue("redact_headers"))}
	slug := r.PathValue("slug")
	resp := s.forwardDashboardMirrorJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/mirrors", "", req, s.createMirrorRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/mirrors?action=created", http.StatusSeeOther)
}

func (s *server) dashboardToggleMirrorRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardMirrorsCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	rule, err := s.store.GetMirrorRuleByID(r.Context(), id)
	if err != nil || rule.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	enabled := !rule.Enabled
	resp := s.forwardDashboardMirrorJSON(r, acct, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug)+"/mirrors/"+url.PathEscape(id), id, api.UpdateMirrorRuleRequest{Enabled: &enabled}, s.updateMirrorRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/mirrors?action=toggled", http.StatusSeeOther)
}

func (s *server) dashboardDeleteMirrorRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardMirrorsCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardMirrorJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/mirrors/"+url.PathEscape(id), id, nil, s.deleteMirrorRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/mirrors?action=deleted", http.StatusSeeOther)
}

func splitDashboardValues(raw string) []string {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' })
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func dashboardCheckbox(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func (s *server) forwardDashboardTenantJSON(r *http.Request, acct state.Account, method, path, id, hostname string, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method, req.URL = method, cloneDashboardURL(r.URL, path, "")
	req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(payload)), int64(len(payload))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", r.PathValue("slug"))
	req.SetPathValue("id", id)
	req.SetPathValue("hostname", hostname)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}

func (s *server) forwardDashboardMirrorJSON(r *http.Request, acct state.Account, method, path, id string, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := r.Clone(r.Context())
	req.Method, req.URL = method, cloneDashboardURL(r.URL, path, "")
	req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(payload)), int64(len(payload))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", r.PathValue("slug"))
	req.SetPathValue("id", id)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}
