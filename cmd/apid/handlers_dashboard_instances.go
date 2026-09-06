package main

// Dashboard surface for per-app instances (issue #1397 / G6).
// Instance rows are read from the canonical store; wake method comes from
// the existing wake.boot_started event and restart/parking diagnostics come
// from the owning deployment. The lifecycle buttons remain app-scoped: the
// scheduler owns individual instance transitions.

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardInstanceAction          = "app_instance_action"
	dashboardInstanceCSRFCookie      = "faas_csrf_app_instance"
	dashboardInstancePageLimit       = 50
	dashboardInstanceActionError     = "error"
	dashboardInstanceActionParked    = "parked"
	dashboardInstanceActionWoken     = "woken"
	dashboardInstanceActionRestarted = "restarted"
)

// parseAppInstancesPath recognizes the per-app instances page. The trailing
// slash is accepted to match the other dashboard app subpages.
func parseAppInstancesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/instances"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// renderAppInstances renders the bounded instance fleet and its lifecycle
// controls. The instance list is capped so parked history cannot turn a
// dashboard request into an unbounded scan.
func (s *server) renderAppInstances(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	instances, err := s.store.ListLatestInstancesForApp(ctx, app.ID, dashboardInstancePageLimit)
	if err != nil {
		log.Warn("dashboard renderAppInstances: list instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		instances = nil
	}
	items := s.projectDashboardInstances(ctx, log, instances)

	token := ""
	if s.sessions != nil {
		token, err = middleware.IssueForAuthenticatedNamed(s.sessions, dashboardInstanceAction, acct.ID, dashboardInstanceCSRFCookie)
		if err != nil {
			log.Warn("dashboard renderAppInstances: issue csrf", "account_id", acct.ID, "app_id", app.ID, "err", err)
			token = ""
		} else {
			http.SetCookie(w, &http.Cookie{
				Name: dashboardInstanceCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode,
				MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
			})
		}
	}
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppInstances: count deployed apps", "account_id", acct.ID, "err", err)
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title:   "Instances — " + app.Slug,
		Body:    "instances",
		Account: dashboardAccountView(view, appCount),
		Data: dashboard.AppInstancesData{
			App:          dashboard.AppListItem{Slug: app.Slug, Status: string(app.Status), URL: appURLForDomain(app.Slug, s.domain)},
			AppStatus:    string(app.Status),
			Instances:    items,
			ActionCSRF:   token,
			Action:       dashboardInstanceActionFlash(r),
			WakeID:       r.URL.Query().Get("wake_id"),
			ErrorMessage: "",
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

// projectDashboardInstances enriches each instance with the existing wake
// event and deployment projections. The page is capped at 50 rows, so these
// bounded reads remain predictable even for a long-lived app.
func (s *server) projectDashboardInstances(ctx stdctx, log *slog.Logger, instances []state.Instance) []dashboard.InstancePageItem {
	wakeIDs := make([]string, 0, len(instances))
	for _, instance := range instances {
		if instance.WakeID != "" {
			wakeIDs = append(wakeIDs, instance.WakeID)
		}
	}
	bootMeta := map[string]state.WakeBootMeta{}
	if len(wakeIDs) > 0 {
		if meta, err := s.store.LookupBootStartedForWakes(ctx, wakeIDs); err == nil {
			bootMeta = meta
		} else {
			log.Warn("dashboard renderAppInstances: lookup boot metadata", "err", err)
		}
	}

	deployments := make(map[string]state.Deployment, len(instances))
	for _, instance := range instances {
		if instance.DeploymentID == "" {
			continue
		}
		if _, ok := deployments[instance.DeploymentID]; ok {
			continue
		}
		deployment, err := s.store.DeploymentByID(ctx, instance.DeploymentID)
		if err != nil {
			log.Warn("dashboard renderAppInstances: deployment lookup", "deployment_id", instance.DeploymentID, "err", err)
			continue
		}
		deployments[instance.DeploymentID] = deployment
	}

	items := make([]dashboard.InstancePageItem, 0, len(instances))
	for _, instance := range instances {
		class, glyph, label := dashboard.BadgeFor(state.State(strings.ToLower(instance.State)))
		item := dashboard.InstancePageItem{
			ID: instance.ID, DeploymentID: instance.DeploymentID, State: instance.State,
			StateClass: class, StateGlyph: glyph, StateLabel: label,
			NodeID: instance.NodeID, HostIP: instance.HostIP, RAMMB: instance.RAMMB,
			WakeID:        instance.WakeID,
			StartedAt:     dashboardInstanceTime(instance.StartedAt),
			LastRequestAt: dashboardInstanceTime(instance.LastRequestAt),
			ParkedAt:      dashboardInstanceTime(instance.ParkedAt),
		}
		if instance.TerminalAt != nil {
			item.TerminalAt = dashboardInstanceTime(*instance.TerminalAt)
		}
		if meta, ok := bootMeta[instance.WakeID]; ok {
			item.WakeMethod, item.WakeTier = meta.Method, meta.Tier
		}
		if deployment, ok := deployments[instance.DeploymentID]; ok {
			item.LivenessRestartCount = deployment.LivenessRestartCount
			item.ParkedReason = deployment.ParkedReason
		}
		items = append(items, item)
	}
	return items
}

func dashboardInstanceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func dashboardInstanceActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case dashboardInstanceActionParked:
		return "parked"
	case dashboardInstanceActionWoken:
		return "woken"
	case dashboardInstanceActionRestarted:
		return "restarted"
	case dashboardInstanceActionError:
		return "error"
	default:
		return ""
	}
}

// dashboardInstanceAction handles the app-scoped park, wake, and restart
// buttons. Each form is protected by a named CSRF envelope and delegates to
// the same store/notification transitions as the v1 lifecycle endpoints.
func (s *server) dashboardInstanceAction(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	slug, action := r.PathValue("slug"), r.PathValue("action")
	if !validSlug(slug) || !dashboardInstanceActionValid(action) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardInstanceAction, acct.ID, dashboardInstanceCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}

	var wakeID string
	switch action {
	case "park":
		st := state.AppEvictedCold
		if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not park app"))
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged, fmt.Sprintf(`{"kind":"parked","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
		s.log.Info("dashboard app parked", "app", app.ID, "account", acct.ID)
		returnDashboardInstanceAction(w, r, slug, dashboardInstanceActionParked, "")
	case "wake":
		st := state.AppActive
		if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not wake app"))
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged, fmt.Sprintf(`{"kind":"woken","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
		s.log.Info("dashboard app woken", "app", app.ID, "account", acct.ID)
		returnDashboardInstanceAction(w, r, slug, dashboardInstanceActionWoken, "")
	case "restart":
		if app.Status != state.AppActive {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"App is not active", "only an active app can be restarted"))
			return
		}
		st := state.AppEvictedCold
		if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not restart app"))
			return
		}
		wakeUUID, err := uuid.NewV7()
		if err != nil {
			wakeUUID = uuid.New()
		}
		wakeID = wakeUUID.String()
		if err := s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"restart","slug":"%s","app_id":"%s","wake_id":"%s"}`, app.Slug, app.ID, wakeID)); err != nil {
			s.log.Warn("dashboard app restart: notify schedd failed", "app", app.ID, "err", err)
		}
		s.log.Info("dashboard app restart requested", "app", app.ID, "account", acct.ID, "wake_id", wakeID)
		returnDashboardInstanceAction(w, r, slug, dashboardInstanceActionRestarted, wakeID)
	}
}

func dashboardInstanceActionValid(action string) bool {
	return action == "park" || action == "wake" || action == "restart"
}

func returnDashboardInstanceAction(w http.ResponseWriter, r *http.Request, slug, action, wakeID string) {
	values := url.Values{}
	values.Set("action", action)
	if wakeID != "" {
		values.Set("wake_id", wakeID)
	}
	http.Redirect(w, r, "/dashboard/apps/"+slug+"/instances?"+values.Encode(), http.StatusSeeOther)
}
