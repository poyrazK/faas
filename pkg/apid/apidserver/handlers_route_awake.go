package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeawake"
	"github.com/onebox-faas/faas/pkg/state"
)

// getRouteAwake serves GET /v1/apps/{slug}/routes/awake (ADR-1187): how long
// the app was awake in the window, which routes kept it awake, and what each
// busy route's requests cost in awake time. It never writes.
func (s *server) getRouteAwake(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if r.URL.Query().Get("since") == "" {
		q := r.URL.Query()
		q.Set("since", api.RouteAwakeDefaultSince.String())
		r.URL.RawQuery = q.Encode()
	}
	window, err := routeCustomerWindow(r, time.Now().UTC(), time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(routeawake.Store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route awake report"))
		return
	}
	data, err := routeawake.Load(r.Context(), store, acct.ID, app.ID, window.From, window.Until)
	if err != nil {
		s.log.Warn("route awake read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("route awake report"))
		return
	}
	out := routeawake.Analyze(routeawake.Inputs{
		App: app.Slug, From: window.From, Until: window.Until,
		IdleTimeoutS: limits.EffectiveIdleTimeoutS(string(app.Type), app.IdleTimeoutS), RAMMB: app.RAMMB,
		Awake: data.Awake, Routes: data.Routes, Sources: data.Sources,
	})
	out.Slug, out.WindowClamped, out.MinInstances = app.Slug, window.WindowClamped, app.MinInstances
	out.From, out.Until = window.From.Format(time.RFC3339Nano), window.Until.Format(time.RFC3339Nano)
	writeJSON(w, http.StatusOK, out)
}
