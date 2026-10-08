package main

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderServiceMapDashboard serves GET /dashboard/service-map (ADR-732). It
// renders the same map as GET /v1/service-map; an invalid ?range= falls back
// to the default instead of failing the page.
func (s *server) renderServiceMapDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data, err := s.dashboardServiceMapData(r, acct)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	page := dashboard.Page{Title: "Service map", Body: "service_map", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardServiceMapData(r *http.Request, acct state.Account) (dashboard.ServiceMapData, error) {
	data := dashboard.ServiceMapData{
		Enabled:          s.serviceMapEnabled,
		PlanAllowed:      acct.Plan.PerAppMetricsAllowed(),
		Range:            api.ServiceMapDefaultRange,
		Ranges:           appmetrics.Ranges(),
		HighErrorRatePct: api.ServiceMapHighErrorRatePct,
	}
	if !data.Enabled || !data.PlanAllowed {
		return data, nil
	}
	if rng := r.URL.Query().Get("range"); appmetrics.IsValidRange(rng) {
		data.Range = rng
	}
	m, err := s.serviceMapFor(r.Context(), acct, data.Range)
	if err != nil {
		return data, err
	}
	if reason, degraded := strings.CutPrefix(m.Source, appmetrics.SourceDegradedPrefix); degraded {
		data.Degraded = reason
	}
	data.Map = &m
	return data, nil
}
