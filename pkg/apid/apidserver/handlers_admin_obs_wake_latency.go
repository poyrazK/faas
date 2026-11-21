package apidserver

// Operator wake-latency report (ADR-20261121200409).
//
//	GET /v1/admin/obs/wake-latency?window=1h&node=&app_id=

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/wakelatency"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsWakeLatency(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	in, prob := parseWakeLatencyQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	reader, ok := s.store.(state.EventsByKindReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("wake events unavailable"))
		return
	}
	look, err := s.newWakeLookup(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list compute nodes"))
		return
	}
	if node := r.URL.Query().Get("node"); node != "" {
		if in.NodeID = look.nodeIDs[node]; in.NodeID == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", "no compute node has this name"))
			return
		}
	}
	in.Events = map[string][]state.Event{}
	for _, kind := range wakelatency.Kinds {
		rows, err := reader.ListEventsByKindSince(r.Context(), kind, in.Since, api.ObsWakeLatencyEventsPerKind+1)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read wake events"))
			return
		}
		if len(rows) > api.ObsWakeLatencyEventsPerKind {
			rows = rows[:api.ObsWakeLatencyEventsPerKind]
			in.Truncated = append(in.Truncated, kind)
		}
		in.Events[kind] = rows
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, wakelatency.Build(in, look))
}

func parseWakeLatencyQuery(r *http.Request) (wakelatency.Input, *api.Problem) {
	q := r.URL.Query()
	in := wakelatency.Input{Window: q.Get("window"), Now: time.Now().UTC(), AppID: q.Get("app_id")}
	window := api.ObsWakeLatencyWindowDefault
	if in.Window == "" {
		in.Window = "1h"
	} else {
		d, err := time.ParseDuration(in.Window)
		if err != nil || d < time.Minute || d > api.ObsWakeLatencyWindowMax {
			return in, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad window", "window must be a duration from 1m to 24h, such as 1h")
		}
		window = d
	}
	if in.AppID != "" {
		if _, err := uuid.Parse(in.AppID); err != nil {
			return in, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad app_id", "app_id must be a UUID")
		}
	}
	in.Since = in.Now.Add(-window)
	return in, nil
}

// wakeLookup names the report's nodes and apps, reading each app once.
type wakeLookup struct {
	ctx       context.Context
	s         *server
	nodeNames map[string]string
	nodeIDs   map[string]string
	apps      map[string]state.App
}

func (s *server) newWakeLookup(ctx context.Context) (*wakeLookup, error) {
	nodes, err := s.store.ListComputeNodes(ctx, true)
	if err != nil {
		return nil, err
	}
	l := &wakeLookup{ctx: ctx, s: s, nodeNames: map[string]string{}, nodeIDs: map[string]string{}, apps: map[string]state.App{}}
	for _, n := range nodes {
		l.nodeNames[n.ID], l.nodeIDs[n.Name] = n.Name, n.ID
	}
	return l, nil
}

func (l *wakeLookup) NodeName(id string) string { return l.nodeNames[id] }

func (l *wakeLookup) App(id string) (string, string) {
	if id == "" {
		return "", ""
	}
	app := l.s.cachedApp(l.ctx, l.apps, id)
	return app.Slug, app.AccountID
}
