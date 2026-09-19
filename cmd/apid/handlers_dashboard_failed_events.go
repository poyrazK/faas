package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardFailedEventsAction     = "failed_events_action"
	dashboardFailedEventsCSRFCookie = "faas_csrf_failed_events"
	dashboardFailedEventsPerApp     = 50
	dashboardFailedEventsMax        = 200
)

func (s *server) renderFailedEvents(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard failed events: list apps", "account_id", acct.ID, "err", err)
		apps = nil
	}
	selected := strings.TrimSpace(r.URL.Query().Get("app"))
	data := dashboard.FailedEventsData{SelectedApp: selected, Action: failedEventsActionFlash(r)}
	appIDs := make(map[string]string, len(apps))
	appSlugs := make(map[string]string, len(apps))
	for _, app := range apps {
		appIDs[app.Slug] = app.ID
		appSlugs[app.ID] = app.Slug
		data.Apps = append(data.Apps, dashboard.AppListItem{
			Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain),
		})
	}
	if selected != "" {
		if _, found := appIDs[selected]; !found {
			http.NotFound(w, r)
			return
		}
	}
	events, listErr := s.store.ListDeadLetterEventsForAccount(ctx, acct.ID, dashboardFailedEventsMax, "")
	if listErr != nil {
		log.Warn("dashboard failed events: list account events", "account_id", acct.ID, "err", listErr)
	} else {
		for _, event := range events {
			appSlug := appSlugs[event.AppID]
			if selected != "" && appSlug != selected {
				continue
			}
			data.Events = append(data.Events, projectFailedEvent(appSlug, event))
		}
	}
	sort.SliceStable(data.Events, func(i, j int) bool {
		return data.Events[i].LastFailed > data.Events[j].LastFailed
	})
	if len(data.Events) > dashboardFailedEventsMax {
		data.Events = data.Events[:dashboardFailedEventsMax]
	}
	if s.sessions != nil {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardFailedEventsAction, acct.ID, dashboardFailedEventsCSRFCookie)
		if tokenErr != nil {
			log.Warn("dashboard failed events: issue csrf", "account_id", acct.ID, "err", tokenErr)
		} else {
			data.ActionCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardFailedEventsCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{Title: "Failed Events", Body: "failed_events", Account: dashboardAccountView(view, len(apps)), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectFailedEvent(appSlug string, event state.DeadLetterEvent) dashboard.FailedEventPageItem {
	status := "open"
	replayedAt := ""
	if event.ReplayedAt != nil {
		status = "replayed"
		replayedAt = dashboardJobsTime(*event.ReplayedAt)
	}
	return dashboard.FailedEventPageItem{
		ID: event.ID, AppSlug: appSlug, Source: event.Source, Origin: event.Origin,
		ErrorKind: event.ErrorKind, Payload: dashboardFailedEventJSON(event.Payload),
		Headers: dashboardFailedEventJSON(event.Headers), ErrorDetail: dashboardFailedEventJSON(event.ErrorDetail),
		RetryCount: event.RetryCount, FirstFailed: dashboardJobsTime(event.FirstFailedAt),
		LastFailed: dashboardJobsTime(event.LastFailedAt), ReplayedAt: replayedAt, Status: status,
	}
}

func dashboardFailedEventJSON(raw []byte) string {
	const maxBytes = 4096
	if len(raw) > maxBytes {
		return string(raw[:maxBytes]) + "…"
	}
	return string(raw)
}

func failedEventsActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "replayed", "discarded", "error":
		return r.URL.Query().Get("action")
	default:
		return ""
	}
}

func (s *server) dashboardFailedEventAction(w http.ResponseWriter, r *http.Request, action string) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardFailedEventsAction, acct.ID, dashboardFailedEventsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	slug, eventID := r.PathValue("slug"), r.PathValue("id")
	if !validSlug(slug) || eventID == "" || strings.Contains(eventID, "/") {
		http.NotFound(w, r)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	event, err := s.store.DeadLetterEventByID(r.Context(), app.ID, eventID)
	if err != nil || event.ReplayedAt != nil {
		http.Redirect(w, r, failedEventsRedirect(slug, "error"), http.StatusSeeOther)
		return
	}
	switch action {
	case "replay":
		_, err = s.store.ReplayDeadLetterEvent(r.Context(), acct.ID, app.ID, eventID)
	case "discard":
		err = s.store.DeleteDeadLetterEvent(r.Context(), acct.ID, app.ID, eventID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Warn("dashboard failed event action", "action", action, "account_id", acct.ID, "app_id", app.ID, "event_id", eventID, "err", err)
	}
	flash := action + "ed"
	if err != nil {
		flash = "error"
	}
	http.Redirect(w, r, failedEventsRedirect(slug, flash), http.StatusSeeOther)
}

func (s *server) dashboardAccountFailedEventAction(w http.ResponseWriter, r *http.Request, action string) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardFailedEventsAction, acct.ID, dashboardFailedEventsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	eventID := r.PathValue("id")
	if eventID == "" || strings.Contains(eventID, "/") {
		http.NotFound(w, r)
		return
	}
	event, err := s.store.DeadLetterEventByAccountID(r.Context(), acct.ID, eventID)
	if err != nil || event.ReplayedAt != nil {
		http.Redirect(w, r, failedEventsRedirect("", "error"), http.StatusSeeOther)
		return
	}
	switch action {
	case "replay":
		_, err = s.store.ReplayDeadLetterEventForAccount(r.Context(), acct.ID, eventID)
	case "discard":
		err = s.store.DeleteDeadLetterEventForAccount(r.Context(), acct.ID, eventID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Warn("dashboard account failed event action", "action", action, "account_id", acct.ID, "event_id", eventID, "err", err)
	}
	flash := action + "ed"
	if err != nil {
		flash = "error"
	}
	http.Redirect(w, r, failedEventsRedirect("", flash), http.StatusSeeOther)
}

func failedEventsRedirect(slug, action string) string {
	values := url.Values{"app": []string{slug}, "action": []string{action}}
	return "/dashboard/failed-events?" + values.Encode()
}
