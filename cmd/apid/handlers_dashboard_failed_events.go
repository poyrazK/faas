package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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
	dashboardFailedEventsPageSize   = 50
)

func (s *server) renderFailedEvents(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard failed events: list apps", "account_id", acct.ID, "err", err)
		apps = nil
	}
	selected := strings.TrimSpace(r.URL.Query().Get("app"))
	data := dashboard.FailedEventsData{
		SelectedApp: selected, Action: failedEventsActionFlash(r), ActionCount: failedEventsActionCount(r),
	}
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
	var events []state.DeadLetterEvent
	var listErr error
	listLimit := dashboardFailedEventsPageSize + 1
	if selected != "" {
		events, listErr = s.store.ListDeadLetterEvents(ctx, appIDs[selected], listLimit, strings.TrimSpace(r.URL.Query().Get("before")))
	} else {
		events, listErr = s.store.ListDeadLetterEventsForAccount(ctx, acct.ID, listLimit, strings.TrimSpace(r.URL.Query().Get("before")))
	}
	if listErr != nil {
		log.Warn("dashboard failed events: list events", "account_id", acct.ID, "app", selected, "err", listErr)
	} else {
		if len(events) > dashboardFailedEventsPageSize {
			data.NextPageURL = failedEventsPageURL(selected, events[dashboardFailedEventsPageSize-1].ID)
			events = events[:dashboardFailedEventsPageSize]
		}
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

func failedEventsActionCount(r *http.Request) int {
	count, err := strconv.Atoi(r.URL.Query().Get("count"))
	if err != nil || count < 0 || count > dashboardFailedEventsMax {
		return 0
	}
	return count
}

func failedEventsPageURL(app, before string) string {
	values := url.Values{"before": []string{before}}
	if app != "" {
		values.Set("app", app)
	}
	return "/dashboard/failed-events?" + values.Encode()
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
		if err == nil {
			err = state.ErrNotFound
		}
		s.observeDashboardDeadLetterAction(app.Slug, action, err)
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
	s.observeDashboardDeadLetterAction(app.Slug, action, err)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Warn("dashboard failed event action", "action", action, "account_id", acct.ID, "app_id", app.ID, "event_id", eventID, "err", err)
	}
	flash := action + "ed"
	if err != nil {
		flash = "error"
	} else if action == "replay" {
		s.audit.Emit(r.Context(), "app.dlq.event_replayed", &acct.ID, map[string]any{
			"app_id": app.ID, "event_id": event.ID, "source": event.Source,
			"source_id": event.SourceID, "surface": "dashboard",
		})
	} else {
		s.audit.Emit(r.Context(), "app.dlq.purged", &acct.ID, map[string]any{
			"app_id": app.ID, "event_id": event.ID, "count": 1,
			"surface": "dashboard",
		})
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
		if err == nil {
			err = state.ErrNotFound
		}
		s.observeDashboardDeadLetterAction("account", action, err)
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
	s.observeDashboardDeadLetterAction("account", action, err)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Warn("dashboard account failed event action", "action", action, "account_id", acct.ID, "event_id", eventID, "err", err)
	}
	flash := action + "ed"
	if err != nil {
		flash = "error"
	} else if action == "replay" {
		s.audit.Emit(r.Context(), "account.dlq.event_replayed", &acct.ID, map[string]any{
			"event_id": event.ID, "source": event.Source, "source_id": event.SourceID,
			"app_id": event.AppID, "account_scope": true, "surface": "dashboard",
		})
	} else {
		s.audit.Emit(r.Context(), "account.dlq.purged", &acct.ID, map[string]any{
			"event_id": event.ID, "count": 1, "account_scope": true,
			"surface": "dashboard",
		})
	}
	http.Redirect(w, r, failedEventsRedirect("", flash), http.StatusSeeOther)
}

func (s *server) dashboardFailedEventsBulkAction(w http.ResponseWriter, r *http.Request, action string) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardFailedEventsAction, acct.ID, dashboardFailedEventsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	selected := strings.TrimSpace(r.FormValue("app"))
	scope := "account"
	var count int
	var err error
	var app state.App
	if selected != "" {
		app, err = s.store.AppBySlug(r.Context(), selected)
		if err != nil || app.AccountID != acct.ID {
			s.observeDashboardDeadLetterAction("account", action, state.ErrNotFound)
			http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
			return
		}
		scope = app.Slug
		switch action {
		case "replay":
			count, err = s.store.ReplayDeadLetterEvents(r.Context(), acct.ID, app.ID, dashboardFailedEventsMax)
		case "discard":
			count, err = s.store.DeleteDeadLetterEvents(r.Context(), acct.ID, app.ID, dashboardFailedEventsMax)
		default:
			http.NotFound(w, r)
			return
		}
	} else {
		switch action {
		case "replay":
			count, err = s.store.ReplayDeadLetterEventsForAccount(r.Context(), acct.ID, dashboardFailedEventsMax)
		case "discard":
			count, err = s.store.DeleteDeadLetterEventsForAccount(r.Context(), acct.ID, dashboardFailedEventsMax)
		default:
			http.NotFound(w, r)
			return
		}
	}
	if err != nil {
		s.observeDashboardDeadLetterAction(scope, action, err)
		s.log.Warn("dashboard failed events bulk action", "action", action, "account_id", acct.ID, "app", selected, "err", err)
		http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
		return
	}
	for i := 0; i < count; i++ {
		s.observeDashboardDeadLetterAction(scope, action, nil)
	}
	if count > 0 {
		kind := "app.dlq.event_replayed"
		if action == "discard" {
			kind = "app.dlq.purged"
		}
		data := map[string]any{"count": count, "operation": "batch", "surface": "dashboard"}
		if selected != "" {
			data["app_id"] = app.ID
		} else {
			data["account_scope"] = true
			if action == "replay" {
				kind = "account.dlq.event_replayed"
			} else {
				kind = "account.dlq.purged"
			}
		}
		s.audit.Emit(r.Context(), kind, &acct.ID, data)
	}
	flash := action + "ed"
	http.Redirect(w, r, failedEventsRedirect(selected, flash, count), http.StatusSeeOther)
}

func (s *server) dashboardFailedEventsSelectedAction(w http.ResponseWriter, r *http.Request, action string) {
	if action != "replay" && action != "discard" {
		http.NotFound(w, r)
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardFailedEventsAction, acct.ID, dashboardFailedEventsCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	selected := strings.TrimSpace(r.FormValue("app"))
	ids := dashboardFailedEventIDs(r.Form["event_id"])
	if len(ids) == 0 || len(ids) > dashboardFailedEventsMax {
		http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
		return
	}

	scope := "account"
	var app state.App
	var err error
	if selected != "" {
		app, err = s.store.AppBySlug(r.Context(), selected)
		if err != nil || app.AccountID != acct.ID {
			s.observeDashboardDeadLetterAction("account", action, state.ErrNotFound)
			http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
			return
		}
		scope = app.Slug
	}

	events := make([]state.DeadLetterEvent, 0, len(ids))
	for _, id := range ids {
		var event state.DeadLetterEvent
		if selected != "" {
			event, err = s.store.DeadLetterEventByID(r.Context(), app.ID, id)
		} else {
			event, err = s.store.DeadLetterEventByAccountID(r.Context(), acct.ID, id)
		}
		if err != nil || event.ReplayedAt != nil {
			if err == nil {
				err = state.ErrNotFound
			}
			s.observeDashboardDeadLetterAction(scope, action, err)
			http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
			return
		}
		events = append(events, event)
	}

	for _, event := range events {
		if selected != "" {
			switch action {
			case "replay":
				_, err = s.store.ReplayDeadLetterEvent(r.Context(), acct.ID, app.ID, event.ID)
			case "discard":
				err = s.store.DeleteDeadLetterEvent(r.Context(), acct.ID, app.ID, event.ID)
			}
		} else {
			switch action {
			case "replay":
				_, err = s.store.ReplayDeadLetterEventForAccount(r.Context(), acct.ID, event.ID)
			case "discard":
				err = s.store.DeleteDeadLetterEventForAccount(r.Context(), acct.ID, event.ID)
			}
		}
		s.observeDashboardDeadLetterAction(scope, action, err)
		if err != nil {
			s.log.Warn("dashboard failed events selected action", "action", action, "account_id", acct.ID, "app", selected, "event_id", event.ID, "err", err)
			http.Redirect(w, r, failedEventsRedirect(selected, "error"), http.StatusSeeOther)
			return
		}
	}

	kind := "app.dlq.event_replayed"
	if action == "discard" {
		kind = "app.dlq.purged"
	}
	data := map[string]any{"count": len(events), "operation": "batch", "selection": "selected", "surface": "dashboard"}
	if selected != "" {
		data["app_id"] = app.ID
	} else {
		data["account_scope"] = true
		if action == "replay" {
			kind = "account.dlq.event_replayed"
		} else {
			kind = "account.dlq.purged"
		}
	}
	s.audit.Emit(r.Context(), kind, &acct.ID, data)
	http.Redirect(w, r, failedEventsRedirect(selected, action+"ed", len(events)), http.StatusSeeOther)
}

func dashboardFailedEventIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (s *server) observeDashboardDeadLetterAction(app, action string, err error) {
	status := "success"
	if err != nil {
		status = "error"
		if errors.Is(err, state.ErrNotFound) {
			status = "not_found"
		}
	}
	switch action {
	case "replay":
		s.ops.ObserveDLQReplay(app, status)
	case "discard":
		s.ops.ObserveDLQPurge(app, status)
	}
}

func failedEventsRedirect(slug, action string, count ...int) string {
	values := url.Values{"app": []string{slug}, "action": []string{action}}
	if len(count) > 0 {
		values.Set("count", strconv.Itoa(count[0]))
	}
	return "/dashboard/failed-events?" + values.Encode()
}
