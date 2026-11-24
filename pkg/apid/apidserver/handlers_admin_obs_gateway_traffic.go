package apidserver

// Operator gateway traffic report (ADR-20261124093000).
//
//	GET /v1/admin/obs/gateway-traffic?window=1h

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/gatewaytraffic"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsGatewayTraffic(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	window, prob := parseGatewayTrafficWindow(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	in := gatewaytraffic.Input{Now: now, Window: window, Samples: gatewaytraffic.Read(ctx, s.promqlClient, window, now)}
	in.Lookup = func(appID string) (gatewaytraffic.AppInfo, bool) {
		app, err := s.store.AppByID(ctx, appID)
		if err != nil {
			return gatewaytraffic.AppInfo{}, false
		}
		return gatewaytraffic.AppInfo{Slug: app.Slug, AccountID: app.AccountID}, true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, gatewaytraffic.Build(in))
}

func parseGatewayTrafficWindow(r *http.Request) (time.Duration, *api.Problem) {
	raw := r.URL.Query().Get("window")
	if raw == "" {
		return api.ObsGatewayTrafficWindowDefault, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < api.ObsGatewayTrafficWindowMin || d > api.ObsGatewayTrafficWindowMax {
		return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad window", "window must be a duration from 5m to 168h, such as 1h")
	}
	return d, nil
}
