package main

// Dashboard surface for outbound webhooks (issue #1397 / G8). The page is a
// thin form adapter over the existing /v1/apps/{slug}/webhooks handlers: API
// validation, plan gates, ownership checks, auditing, and RFC 7807 problems
// remain the single source of truth.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardWebhooksAction       = "app_webhooks_mutation"
	dashboardWebhooksCSRFCookie   = "faas_csrf_app_webhooks"
	dashboardWebhooksPageLimit    = 50
	dashboardWebhookDeliveryLimit = 20
)

// parseAppWebhooksPath recognizes the per-app outbound-webhooks page.
func parseAppWebhooksPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/webhooks"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

func (s *server) renderAppWebhooks(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	limits, planKnown := api.LimitsFor(acct.Plan)
	data := dashboard.AppWebhooksData{
		App:           dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
		PlanAllowed:   planKnown && limits.WebhookPerApp > 0,
		Events:        append([]string(nil), api.AllowedAppWebhookEvents...),
		RetryPolicies: append([]string(nil), api.AllowedAppWebhookRetryPolicies...),
		Action:        dashboardWebhooksActionFlash(r),
	}
	if data.PlanAllowed {
		rows, listErr := s.store.ListAppWebhooksForApp(ctx, app.ID)
		if listErr != nil {
			data.ErrorMessage = "Webhook data is temporarily unavailable. Please try again shortly."
			log.Warn("dashboard webhooks: list subscriptions", "account_id", acct.ID, "app_id", app.ID, "err", listErr)
		} else {
			if len(rows) > dashboardWebhooksPageLimit {
				rows = rows[:dashboardWebhooksPageLimit]
			}
			data.Webhooks = s.projectDashboardWebhooks(ctx, log, rows, app.ID, acct.ID)
		}
	}

	if s.sessions != nil {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardWebhooksAction, acct.ID, dashboardWebhooksCSRFCookie)
		if tokenErr != nil {
			log.Warn("dashboard webhooks: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{
				Name: dashboardWebhooksCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode,
				MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
			})
		}
	}

	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard webhooks: count apps", "account_id", acct.ID, "err", countErr)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title:   "Webhooks — " + app.Slug,
		Body:    "webhooks",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) projectDashboardWebhooks(ctx context.Context, log *slog.Logger, rows []state.AppWebhook, appID, accountID string) []dashboard.WebhookPageItem {
	items := make([]dashboard.WebhookPageItem, 0, len(rows))
	for _, row := range rows {
		if row.AppID != appID || row.AccountID != accountID {
			continue
		}
		item := dashboard.WebhookPageItem{
			ID: row.ID, TargetURL: row.TargetURL, EventFilter: append([]string(nil), row.EventFilter...),
			RetryPolicy: string(row.RetryPolicy), Enabled: row.Enabled,
			CreatedAt: dashboardJobsTime(row.CreatedAt), UpdatedAt: dashboardJobsTime(row.UpdatedAt),
		}
		deliveries, _, err := s.store.ListAppWebhookDeliveries(ctx, row.AppID, row.ID, dashboardWebhookDeliveryLimit, "")
		if err != nil {
			log.Warn("dashboard webhooks: list deliveries", "account_id", accountID, "app_id", appID, "webhook_id", row.ID, "err", err)
		} else {
			item.Deliveries = projectDashboardWebhookDeliveries(deliveries, row.ID, appID, accountID)
		}
		items = append(items, item)
	}
	return items
}

func projectDashboardWebhookDeliveries(rows []state.AppWebhookDelivery, webhookID, appID, accountID string) []dashboard.WebhookDeliveryPageItem {
	items := make([]dashboard.WebhookDeliveryPageItem, 0, len(rows))
	for _, row := range rows {
		if row.WebhookID != webhookID || row.AppID != appID || row.AccountID != accountID {
			continue
		}
		items = append(items, dashboard.WebhookDeliveryPageItem{
			ID: row.ID, Event: string(row.Event), Attempt: row.Attempt, Status: string(row.Status),
			LastError: row.LastError, LastResponseCode: row.LastResponseCode,
			NextAttemptAt: dashboardJobsTime(row.NextAttemptAt), DeliveredAt: dashboardWebhookOptionalTime(row.DeliveredAt),
			CreatedAt: dashboardJobsTime(row.CreatedAt), Retryable: row.Status == state.AppWebhookDeliveryDead,
		})
	}
	return items
}

func dashboardWebhookOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return dashboardJobsTime(*value)
}

func dashboardWebhooksActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "created", "toggled", "deleted", "rotated", "replayed":
		return r.URL.Query().Get("action")
	case "error":
		return "error"
	default:
		return ""
	}
}

func (s *server) verifyDashboardWebhooksCSRF(w http.ResponseWriter, r *http.Request, accountID string) bool {
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardWebhooksAction, accountID, dashboardWebhooksCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return false
	}
	return true
}

func (s *server) dashboardCreateAppWebhook(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardWebhooksCSRF(w, r, acct.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse webhook form"))
		return
	}
	req := api.CreateAppWebhookRequest{
		TargetURL:     strings.TrimSpace(r.FormValue("target_url")),
		WebhookSecret: r.FormValue("webhook_secret"),
		RetryPolicy:   strings.TrimSpace(r.FormValue("retry_policy")),
	}
	if enabled := strings.TrimSpace(r.FormValue("enabled")); enabled != "" {
		value := enabled == "true"
		req.Enabled = &value
	}
	if events := r.Form["event_filter"]; len(events) > 0 {
		req.EventFilter = append([]string(nil), events...)
	}
	slug := r.PathValue("slug")
	resp := s.forwardDashboardWebhookJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/webhooks", "", "", req, s.createAppWebhook)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/webhooks?action=created", http.StatusSeeOther)
}

func (s *server) dashboardToggleAppWebhook(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardWebhooksCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	row, err := s.store.AppWebhookByID(r.Context(), id)
	if err != nil || row.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	if app, appErr := s.store.AppBySlug(r.Context(), slug); appErr != nil || app.ID != row.AppID {
		http.NotFound(w, r)
		return
	}
	enabled := !row.Enabled
	resp := s.forwardDashboardWebhookJSON(r, acct, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug)+"/webhooks/"+url.PathEscape(id), id, "", api.UpdateAppWebhookRequest{Enabled: &enabled}, s.updateAppWebhook)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/webhooks?action=toggled", http.StatusSeeOther)
}

func (s *server) dashboardDeleteAppWebhook(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardWebhooksCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardWebhookJSON(r, acct, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/webhooks/"+url.PathEscape(id), id, "", nil, s.deleteAppWebhook)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/webhooks?action=deleted", http.StatusSeeOther)
}

func (s *server) dashboardRotateAppWebhookSecret(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardWebhooksCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	resp := s.forwardDashboardWebhookJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/webhooks/"+url.PathEscape(id)+"/rotate-secret", id, "", nil, s.rotateAppWebhookSecret)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/webhooks?action=rotated", http.StatusSeeOther)
}

func (s *server) dashboardRetryAppWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.verifyDashboardWebhooksCSRF(w, r, acct.ID) {
		return
	}
	slug, id, did := r.PathValue("slug"), r.PathValue("id"), r.PathValue("did")
	resp := s.forwardDashboardWebhookJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/webhooks/"+url.PathEscape(id)+"/deliveries/"+url.PathEscape(did)+"/retry", id, did, nil, s.retryAppWebhookDelivery)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/webhooks?action=replayed", http.StatusSeeOther)
}

func (s *server) forwardDashboardWebhookJSON(r *http.Request, acct state.Account, method, path, id, did string, body any, handler dashboardJSONHandler) *httptest.ResponseRecorder {
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
	req.SetPathValue("did", did)
	resp := httptest.NewRecorder()
	handler(resp, req, acct)
	return resp
}
