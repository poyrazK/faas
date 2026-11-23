package apidserver

// Operator build queue report (ADR-20261123163340).
//
//	GET /v1/admin/obs/build-queue?window=24h

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/buildqueue"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsBuildQueue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	in, prob := parseBuildQueueQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	reader, ok := s.store.(state.BuildQueueReportReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("build queue unavailable"))
		return
	}
	active, err := reader.OperatorActiveBuilds(r.Context(), api.ObsBuildQueueActiveMax+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read active builds"))
		return
	}
	if len(active) > api.ObsBuildQueueActiveMax {
		active, in.ActiveTruncated = active[:api.ObsBuildQueueActiveMax], true
	}
	finished, err := reader.OperatorFinishedBuildsSince(r.Context(), in.Now.Add(-in.Window), api.ObsBuildQueueFinishedMax+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read finished builds"))
		return
	}
	if len(finished) > api.ObsBuildQueueFinishedMax {
		finished, in.FinishedTruncated = finished[:api.ObsBuildQueueFinishedMax], true
	}
	in.Active, in.Finished = active, finished
	in.Slots, in.SlotsState = buildqueue.ReadSlots(r.Context(), s.promqlClient)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, buildqueue.Build(in))
}

func parseBuildQueueQuery(r *http.Request) (buildqueue.Input, *api.Problem) {
	in := buildqueue.Input{Now: time.Now().UTC(), Window: api.ObsBuildQueueWindowDefault}
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Minute || d > api.ObsBuildQueueWindowMax {
			return in, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad window", "window must be a duration from 1m to 168h, such as 24h")
		}
		in.Window = d
	}
	return in, nil
}
