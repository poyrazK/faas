package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	sidecarTimelineLimitDefault = 200
	sidecarTimelineLimitMax     = 1000
)

// listSidecarTimeline handles GET
// /v1/apps/{slug}/sidecars/{sidecar_name}/timeline. The timeline combines
// init-exit, restart, and health-transition frames for one sidecar and also
// hoists the latest health transition into a small status snapshot.
func (s *server) listSidecarTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	target, ok := s.resolveOnBehalfOf(w, r, acct, "sidecar-timeline")
	if !ok {
		return
	}
	authAcct := acct
	if target != nil {
		authAcct = *target
	}
	if !authAcct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(authAcct.Plan))
		return
	}

	slug := r.PathValue("slug")
	sidecarName := r.PathValue("sidecar_name")
	if sidecarName == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid sidecar_name", "sidecar_name path segment is required"))
		return
	}
	app, ok := s.loadApp(w, r, authAcct, slug)
	if !ok {
		return
	}

	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			t, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-07-25T00:00:00Z)"))
			return
		}
		since = t
	}

	limit := sidecarTimelineLimitDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > sidecarTimelineLimitMax {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be between 1 and "+strconv.Itoa(sidecarTimelineLimitMax)))
			return
		}
		limit = n
	}

	rows, err := s.store.ListEventsBySidecar(r.Context(), sidecarName, since, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list sidecar timeline"))
		return
	}

	// The sidecar-name query is intentionally account-agnostic because the
	// events table is append-only. Forge-proof every payload before exposing
	// it under the slug-resolved app, just as the wake timeline does.
	visible := make([]state.Event, 0, limit)
	for _, event := range rows {
		if !eventDataHasAppID(event.Data, app.ID) {
			continue
		}
		if len(visible) >= limit {
			break
		}
		visible = append(visible, event)
	}
	if len(visible) == 0 && since.IsZero() {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Sidecar timeline not found", "no events row with that sidecar_name belongs to this account"))
		return
	}

	events := make([]api.WakeTimelineEvent, 0, len(visible))
	var latest *api.SidecarTimelineStatus
	for _, event := range visible {
		wireEvent := wakeTimelineEvent(event)
		events = append(events, wireEvent)
		if event.Kind != "wake.sidecar_health" {
			continue
		}
		var payload struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || payload.Status == "" {
			continue
		}
		latest = &api.SidecarTimelineStatus{
			At:     event.At.UTC().Format(time.RFC3339Nano),
			Status: payload.Status,
			Reason: payload.Reason,
		}
	}

	var nextCursor string
	if len(visible) > 0 && len(rows) > limit {
		nextCursor = rows[limit-1].At.UTC().Format(time.RFC3339Nano)
	}
	if target != nil {
		emitOperatorActionView(r, s, acct, target.ID, "sidecar-timeline")
	}
	writeJSON(w, http.StatusOK, api.SidecarTimelineResponse{
		SidecarName: sidecarName,
		AppID:       app.ID,
		Latest:      latest,
		Events:      events,
		NextCursor:  nextCursor,
		Limit:       limit,
	})
}
