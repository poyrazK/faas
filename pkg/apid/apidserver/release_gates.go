// adr: 20261019071734 — release gates on customer signals.
// adr: 20261022082305 — synthetic and metric_change gates.
package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) releaseGateStores(w http.ResponseWriter) (state.ReleaseGateStore, state.SLOStore, bool) {
	gates, ok := s.store.(state.ReleaseGateStore)
	slos, sloOK := s.store.(state.SLOStore)
	if !ok || !sloOK {
		api.WriteProblem(w, api.ErrCapacity("release gates are unavailable"))
		return nil, nil, false
	}
	return gates, slos, true
}

// releaseGateView renders a gate with its SLO's or synthetic check's name.
func releaseGateView(g state.ReleaseGate, names map[string]string) api.ReleaseGate {
	out := api.ReleaseGate{Name: g.Name, Kind: g.Kind, MinSamples: g.MinSamples, CreatedBy: g.CreatedBy,
		CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339)}
	switch g.Kind {
	case state.ReleaseGateSLO:
		out.SLO, out.MaxBurnRate = names[g.SLOID], g.MaxBurnRate
	case state.ReleaseGateNewErrors:
		maxGroups := g.MaxNewGroups
		out.MaxNewGroups, out.MinEvents = &maxGroups, g.MinEvents
	case state.ReleaseGateCustomMetric:
		threshold := g.Threshold
		out.Metric, out.Comparison, out.Threshold = g.MetricName, g.Comparison, &threshold
	case state.ReleaseGateSynthetic:
		maxFailure := g.MaxFailurePercent
		out.Check, out.MaxFailurePercent = names[g.SyntheticCheckID], &maxFailure
	case state.ReleaseGateMetricChange:
		out.Metric, out.Comparison, out.MaxChangePercent = g.MetricName, g.Comparison, g.MaxChangePercent
	}
	return out
}

// appSLONames maps the app's SLO and synthetic check IDs to their names;
// the IDs are UUIDs, so the two never collide.
func (s *server) appSLONames(r *http.Request, slos state.SLOStore, appID string) map[string]string {
	names := map[string]string{}
	if list, err := slos.ListSLOs(r.Context(), appID); err == nil {
		for _, slo := range list {
			names[slo.ID] = slo.Name
		}
	}
	if checks, ok := s.store.(state.SyntheticCheckStore); ok {
		if list, err := checks.ListSyntheticChecks(r.Context(), appID); err == nil {
			for _, c := range list {
				names[c.ID] = c.Name
			}
		}
	}
	return names
}

// resolveReleaseGateTarget fills a gate's SLO or synthetic check from the
// names in the request.
func (s *server) resolveReleaseGateTarget(r *http.Request, gate *state.ReleaseGate, spec releasegate.Spec, slos state.SLOStore) *api.Problem {
	switch spec.Kind {
	case releasegate.KindSLO:
		list, err := slos.ListSLOs(r.Context(), gate.AppID)
		if err != nil {
			return api.ErrCapacity("could not read the app's SLOs")
		}
		for _, slo := range list {
			if slo.Name == spec.SLOName {
				gate.SLOID = slo.ID
			}
		}
		if gate.SLOID == "" {
			return api.ErrValidation("the app has no SLO named " + spec.SLOName + "; list SLOs with `gregale slo list`")
		}
	case releasegate.KindSynthetic:
		checks, ok := s.store.(state.SyntheticCheckStore)
		if !ok {
			return api.ErrCapacity("synthetic checks are unavailable")
		}
		list, err := checks.ListSyntheticChecks(r.Context(), gate.AppID)
		if err != nil {
			return api.ErrCapacity("could not read the app's synthetic checks")
		}
		for _, c := range list {
			if c.Name == spec.CheckName {
				gate.SyntheticCheckID = c.ID
			}
		}
		if gate.SyntheticCheckID == "" {
			return api.ErrValidation("the app has no synthetic check named " + spec.CheckName + "; list checks with `gregale synthetics list`")
		}
	}
	return nil
}

// listReleaseGates serves GET /v1/apps/{slug}/release-gates.
func (s *server) listReleaseGates(w http.ResponseWriter, r *http.Request, acct state.Account) {
	gates, slos, ok := s.releaseGateStores(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := gates.ListReleaseGates(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list release gates"))
		return
	}
	names := s.appSLONames(r, slos, app.ID)
	out := api.ListReleaseGatesResponse{Gates: make([]api.ReleaseGate, 0, len(rows))}
	for _, g := range rows {
		out.Gates = append(out.Gates, releaseGateView(g, names))
	}
	writeJSON(w, http.StatusOK, out)
}

// createReleaseGate serves POST /v1/apps/{slug}/release-gates.
func (s *server) createReleaseGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	gates, slos, ok := s.releaseGateStores(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateReleaseGateRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	spec, prob := releasegate.FromRequest(req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	gate := state.ReleaseGate{AccountID: acct.ID, AppID: app.ID, Name: spec.Name, Kind: spec.Kind,
		MaxBurnRate: spec.MaxBurnRate, MaxNewGroups: spec.MaxNewGroups, MinEvents: spec.MinEvents,
		MetricName: spec.MetricName, Comparison: spec.Comparison, Threshold: spec.Threshold,
		MinSamples: spec.MinSamples, CreatedBy: configActor(r, acct), MaxFailurePercent: spec.MaxFailurePercent,
		MaxChangePercent: spec.MaxChangePercent}
	if p := s.resolveReleaseGateTarget(r, &gate, spec, slos); p != nil {
		api.WriteProblem(w, p)
		return
	}
	created, err := gates.CreateReleaseGate(r.Context(), gate, api.ReleaseGatesPerAppMax, time.Now().UTC())
	switch {
	case errors.Is(err, state.ErrReleaseGateLimit):
		api.WriteProblem(w, api.ErrReleaseGateLimit(api.ReleaseGatesPerAppMax))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Release gate exists",
			"The app already has a release gate named "+spec.Name+"."))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not create release gate"))
		return
	}
	s.audit.Emit(r.Context(), "release_gate.created", &acct.ID, map[string]any{
		"app": app.ID, "gate": created.Name, "kind": created.Kind, "actor": created.CreatedBy,
	})
	writeJSON(w, http.StatusCreated, releaseGateView(created, s.appSLONames(r, slos, app.ID)))
}

// deleteReleaseGate serves DELETE /v1/apps/{slug}/release-gates/{name}.
func (s *server) deleteReleaseGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	gates, slos, ok := s.releaseGateStores(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	name := r.PathValue("name")
	deleted, err := gates.DeleteReleaseGate(r.Context(), app.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrReleaseGateNotFound(name))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete release gate"))
		return
	}
	s.audit.Emit(r.Context(), "release_gate.deleted", &acct.ID, map[string]any{
		"app": app.ID, "gate": deleted.Name, "kind": deleted.Kind, "actor": configActor(r, acct),
	})
	writeJSON(w, http.StatusOK, releaseGateView(deleted, s.appSLONames(r, slos, app.ID)))
}
