package incidents

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

// fireReadRows bounds the deliveries read for an alert incident. They come
// newest first, so they cover any incident that is open or recent; an old
// episode of a rule that fired often since shows fewer fires.
const fireReadRows = 200

// Get serves GET /v1/incidents/{id}.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	detail, appID, err := s.read(r.Context(), acct, r.PathValue("id"))
	if err != nil {
		s.writeReadErr(w, r, err)
		return
	}
	if appID != "" {
		s.addChanges(r.Context(), acct, appID, &detail)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, detail)
}

// read loads the incident from the store its id names and returns its app
// id, empty for an account-wide alert or a platform incident.
func (s *Service) read(ctx context.Context, acct state.Account, id string) (api.IncidentDetail, string, error) {
	store := s.Host.Store()
	detail := api.IncidentDetail{UnavailableSources: []string{}}
	prefix, key := splitID(id)
	switch prefix {
	case alertPrefix:
		ep, err := store.AlertEpisodeByID(ctx, acct.ID, key)
		if err != nil {
			return detail, "", err
		}
		detail.Incident = alertIncident(ep, s.slugger(ctx, state.App{})(ep.AppID))
		s.addFires(ctx, ep, &detail)
		return detail, ep.AppID, nil
	case routePrefix:
		rm, err := store.RouteMonitorIncidentSummaryByID(ctx, acct.ID, key)
		if err != nil {
			return detail, "", err
		}
		detail.Incident = routeIncident(rm, s.slugger(ctx, state.App{})(rm.AppID))
		return detail, rm.AppID, nil
	case platformPrefix:
		if _, err := uuid.Parse(key); err != nil {
			return detail, "", state.ErrNotFound
		}
		ev, err := store.StatusEventByPublicID(ctx, key)
		if err != nil {
			return detail, "", err
		}
		if ev.Kind != publicstatus.KindIncident {
			return detail, "", state.ErrNotFound
		}
		detail.Incident = platformIncident(ev)
		for _, u := range ev.Updates {
			detail.Updates = append(detail.Updates, api.IncidentUpdate{At: u.At.UTC(), State: string(u.State), Message: u.Message})
		}
		return detail, "", nil
	}
	return detail, "", state.ErrNotFound
}

// addFires lists the notifications the episode sent, newest first, and the
// log context the newest one carried.
func (s *Service) addFires(ctx context.Context, ep state.AlertEpisode, detail *api.IncidentDetail) {
	rows, err := s.Host.Store().ListAlertDeliveriesForRule(ctx, ep.RuleID, fireReadRows, false)
	if err != nil {
		s.Host.Logger().WarnContext(ctx, "incidents: read fires", "rule", ep.RuleID, "err", err)
		detail.UnavailableSources = append(detail.UnavailableSources, "fires")
		return
	}
	end := s.now()
	if ep.ResolvedAt != nil {
		end = *ep.ResolvedAt
	}
	for _, d := range rows {
		if d.FiredAt.Before(ep.OpenedAt) || d.FiredAt.After(end) {
			continue
		}
		var payload struct {
			Event      string          `json:"event"`
			LogContext json.RawMessage `json:"log_context"`
		}
		_ = json.Unmarshal(d.Payload, &payload)
		if payload.Event == "alert.resolved" {
			continue
		}
		detail.Fires = append(detail.Fires, api.IncidentFire{At: d.FiredAt.UTC(), Observed: d.ObservedValue, Status: string(d.Status)})
		if detail.LogContext == nil && len(payload.LogContext) > 0 && string(payload.LogContext) != "null" {
			detail.LogContext = payload.LogContext
		}
		if len(detail.Fires) == api.IncidentFiresMax {
			break
		}
	}
}

// addChanges attaches the app's change timeline from IncidentChangeLookback
// before the incident opened until it resolved (or now), at most the change
// timeline's own window.
func (s *Service) addChanges(ctx context.Context, acct state.Account, appID string, detail *api.IncidentDetail) {
	app, err := s.Host.Store().AppByID(ctx, appID)
	if err != nil || app.AccountID != acct.ID {
		detail.UnavailableSources = append(detail.UnavailableSources, "changes")
		return
	}
	until := s.now()
	if detail.Incident.ResolvedAt != nil && detail.Incident.ResolvedAt.Before(until) {
		// The timeline's end is exclusive; keep a change made as it resolved.
		until = detail.Incident.ResolvedAt.Add(time.Second)
	}
	since := detail.Incident.OpenedAt.Add(-api.IncidentChangeLookback)
	if until.Sub(since) > api.ChangeTimelineMaxWindow {
		since = until.Add(-api.ChangeTimelineMaxWindow)
	}
	timeline, ok := s.Host.Changes(ctx, acct, app, since, until)
	if !ok {
		detail.UnavailableSources = append(detail.UnavailableSources, "changes")
		return
	}
	detail.Changes, detail.ChangesTruncated = timeline.Events, timeline.Truncated
	for _, src := range timeline.UnavailableSources {
		detail.UnavailableSources = append(detail.UnavailableSources, "changes."+src)
	}
}
