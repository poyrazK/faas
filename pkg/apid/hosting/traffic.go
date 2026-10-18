package hosting

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type AnalyticsHost interface {
	HostingAnalyticsEnabled() bool
	HostingTrafficReader() artifacts.TrafficReader
}

func (s Service) GetTraffic(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.guard(w, acct) {
		return
	}
	h, ok := s.Host.(AnalyticsHost)
	if !ok || !h.HostingAnalyticsEnabled() {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_analytics_disabled", "Traffic analytics unavailable", "internal traffic analytics are disabled"))
		return
	}
	if h.HostingTrafficReader() == nil {
		api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_analytics_unconfigured", "Traffic analytics unavailable", "the analytics reader is not configured"))
		return
	}
	site, ok := s.site(w, r, acct)
	if !ok {
		return
	}
	report, err := h.HostingTrafficReader().Read(r.Context(), site.ID, site.OriginID, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(503, "hosting_traffic_unavailable", "Traffic observations unavailable", "the provider query failed or the dataset has no first write yet; this is not zero traffic"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}
