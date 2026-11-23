package apidserver

// Operator memory pressure report (ADR-20261123190507).
//
//	GET /v1/admin/obs/memory-pressure?window=24h

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/memorypressure"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsMemoryPressure(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	in, prob := parseMemoryPressureQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	events, okEvents := s.store.(state.MemoryPressureReader)
	reports, okReports := s.store.(state.NodeMemoryReportStore)
	if !okEvents || !okReports {
		api.WriteProblem(w, api.ErrCapacity("memory pressure unavailable"))
		return
	}
	capacity, err := s.store.OperatorCapacity(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read node capacity"))
		return
	}
	if in.Reports, err = reports.NodeMemoryReports(r.Context(), time.Time{}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read node memory"))
		return
	}
	rows, err := events.OperatorMemoryPressureEvents(r.Context(), in.Now.Add(-in.Window), api.ObsMemoryPressureEventsMax+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read evictions"))
		return
	}
	if len(rows) > api.ObsMemoryPressureEventsMax {
		rows, in.Truncated = rows[:api.ObsMemoryPressureEventsMax], true
	}
	in.Nodes, in.Events = capacity.Nodes, rows
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, memorypressure.Build(in))
}

func parseMemoryPressureQuery(r *http.Request) (memorypressure.Input, *api.Problem) {
	in := memorypressure.Input{Now: time.Now().UTC(), Window: api.ObsMemoryPressureWindowDefault}
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Minute || d > api.ObsMemoryPressureWindowMax {
			return in, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad window", "window must be a duration from 1m to 168h, such as 24h")
		}
		in.Window = d
	}
	return in, nil
}
