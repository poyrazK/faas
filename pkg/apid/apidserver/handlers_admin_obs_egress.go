package apidserver

// Operator egress denials and abuse report (ADR-20261127100000).
//
//	GET /v1/admin/obs/egress?window=24h

import (
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressdenials"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsEgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	window, prob := parseEgressWindow(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	in := egressdenials.Input{Now: now, Window: window, Samples: egressdenials.Read(ctx, s.promqlClient, window, now)}
	in.Lookup = func(appID string) (egressdenials.AppInfo, bool) {
		app, err := s.store.AppByID(ctx, appID)
		if err != nil {
			return egressdenials.AppInfo{}, false
		}
		return egressdenials.AppInfo{Slug: app.Slug, AccountID: app.AccountID}, true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, egressdenials.Build(in))
}

func parseEgressWindow(r *http.Request) (time.Duration, *api.Problem) {
	raw := r.URL.Query().Get("window")
	if raw == "" {
		return api.ObsEgressWindowDefault, nil
	}
	d, err := time.ParseDuration(raw)
	if err == nil && d >= api.ObsEgressWindowMin && d <= api.ObsEgressWindowMax {
		return d, nil
	}
	detail := fmt.Sprintf("window must be a duration from %dm to %dh, such as %dh; got %q",
		int(api.ObsEgressWindowMin.Minutes()), int(api.ObsEgressWindowMax.Hours()), int(api.ObsEgressWindowDefault.Hours()), raw)
	prob := api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad window", detail)
	prob.DocsURL = api.ObsEgressDocsURL
	if err == nil {
		// Out of range: the bound crossed and the value, in seconds.
		limit := api.ObsEgressWindowMax
		if d < api.ObsEgressWindowMin {
			limit = api.ObsEgressWindowMin
		}
		prob = prob.WithLimit(int64(limit.Seconds()), int64(d.Seconds()))
	}
	return 0, prob
}
