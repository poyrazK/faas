package apidserver

// Unified operator incident inbox.
//
// GET /v1/admin/obs/incidents composes the existing bounded operator reads
// into one deterministic, read-only projection. The handler does not create a
// new incident table or write to deployment/job/node state; the source rows
// remain owned by their existing controllers.

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsIncidents(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	q, prob := parseObsIncidentQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if owner, err := uuid.Parse(caller.ID); err == nil {
		q.ownerID = owner.String()
	}
	items, unavailable, err := s.loadObsIncidents(r, q)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load operator incidents"))
		return
	}
	page, nextCursor, prob := paginateObsIncidents(items, q.cursor, q.limit)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	emitOperatorJobsView(r, s, caller, "", "obs.incidents")
	writeJSON(w, http.StatusOK, api.ObsIncidentListResponse{
		GeneratedAt:        time.Now().UTC(),
		UnavailableSources: unavailable,
		Items:              page,
		Limit:              q.limit,
		Since:              q.since,
		SinceClamped:       q.sinceClamped,
		Type:               q.kind,
		Severity:           q.severity,
		NextCursor:         nextCursor,
	})
}

type obsIncidentQuery struct {
	since                                      time.Time
	sinceClamped                               bool
	kind                                       string
	severity                                   string
	cursor                                     string
	limit                                      int
	beforeAt                                   time.Time
	beforeID                                   string
	alertStatus                                string
	acknowledgment, ownership, ownerID, snooze string
}

func parseObsIncidentQuery(r *http.Request) (obsIncidentQuery, *api.Problem) {
	now := time.Now().UTC()
	q := obsIncidentQuery{
		since:    now.Add(-time.Duration(api.ObsIncidentWindowDefaultHours) * time.Hour),
		kind:     strings.TrimSpace(r.URL.Query().Get("type")),
		severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		cursor:   strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-09-12T00:00:00Z)")
		}
		window := time.Duration(api.ObsAdminWindowMaxHours) * time.Hour
		if q.kind == "platform_alert" {
			window = api.ObsAlertHistoryRetention
		}
		floor := now.Add(-window)
		q.since = t.UTC()
		if q.since.Before(floor) {
			q.since = floor
			q.sinceClamped = true
		}
	}
	if q.kind != "" {
		switch q.kind {
		case "deployment", "job_run", "compute_node", "platform_alert":
		default:
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid type", "type must be deployment, job_run, compute_node, or platform_alert")
		}
	}
	if q.severity != "" {
		switch q.severity {
		case "warning", "error", "critical":
		default:
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid severity", "severity must be warning, error, or critical")
		}
	}
	q.acknowledgment = r.URL.Query().Get("acknowledgment")
	q.ownership = r.URL.Query().Get("ownership")
	q.snooze = r.URL.Query().Get("snooze")
	if prob := validateObsTriageFilters(q); prob != nil {
		return obsIncidentQuery{}, prob
	}
	q.alertStatus = strings.TrimSpace(r.URL.Query().Get("alert_status"))
	if q.alertStatus != "" && (q.kind != "platform_alert" || q.alertStatus != "firing" && q.alertStatus != "resolved" && q.alertStatus != "unknown") {
		return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid alert status", "alert_status requires type=platform_alert and must be firing, resolved, or unknown")
	}
	limitProb, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ObsIncidentLimitDefault, api.ObsIncidentLimitMax, "incidents")
	if limitProb != nil {
		return obsIncidentQuery{}, limitProb
	}
	q.limit = limit
	if q.cursor != "" {
		key, err := cursor.Decode(q.cursor)
		if err != nil {
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid cursor", "cursor must be a value returned as next_cursor")
		}
		q.beforeAt, q.beforeID = key.CreatedAt, key.ID
	}
	return q, nil
}

func (s *server) loadObsIncidents(r *http.Request, q obsIncidentQuery) ([]api.ObsIncident, []string, error) {
	now := time.Now().UTC()
	items := []api.ObsIncident{}
	unavailable := []string{}
	if q.kind != "platform_alert" {
		var err error
		items, err = s.store.ListOperatorIncidents(r.Context(), state.OperatorIncidentFilter{
			Since: q.since, StuckBefore: now.Add(-api.ObsIncidentStuckAfter), StaleBefore: now.Add(-state.DefaultHeartbeatStaleness),
			Kind: q.kind, Severity: q.severity, BeforeAt: q.beforeAt, BeforeID: q.beforeID, Limit: q.limit + 1,
		})
		if err != nil {
			return nil, nil, err
		}
		for i := range items {
			operatorobs.Enrich(&items[i])
		}
	}
	if q.kind == "" || q.kind == "platform_alert" {
		alerts, missing, err := operatorobs.AlertIncidents(r.Context(), s.store, s.promqlClient, state.OperatorAlertFilter{
			Acknowledgment: q.acknowledgment, Ownership: q.ownership, OwnerID: q.ownerID, Snooze: q.snooze,
			Now: now, Since: q.since, BeforeAt: q.beforeAt, BeforeID: q.beforeID, Severity: q.severity, Status: q.alertStatus, Limit: q.limit + 1,
		})
		if err != nil {
			return nil, nil, err
		}
		unavailable = append(unavailable, missing...)
		for _, alert := range alerts {
			if q.severity == "" || q.severity == alert.Severity {
				items = append(items, alert)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	return items, unavailable, nil
}

func paginateObsIncidents(items []api.ObsIncident, rawCursor string, limit int) ([]api.ObsIncident, string, *api.Problem) {
	start := 0
	if rawCursor != "" {
		key, err := cursor.Decode(rawCursor)
		if err != nil {
			return nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid cursor", "cursor must be an opaque value returned as next_cursor")
		}
		for start < len(items) {
			item := items[start]
			if item.ObservedAt.Before(key.CreatedAt) || (item.ObservedAt.Equal(key.CreatedAt) && item.ID < key.ID) {
				break
			}
			start++
		}
	}
	if start >= len(items) {
		return []api.ObsIncident{}, "", nil
	}
	remaining := items[start:]
	next := ""
	if len(remaining) > limit {
		page := remaining[:limit]
		last := page[len(page)-1]
		next = cursor.Encode(cursor.Key{CreatedAt: last.ObservedAt, ID: last.ID})
		return page, next, nil
	}
	return remaining, next, nil
}

func validateObsTriageFilters(q obsIncidentQuery) *api.Problem {
	if (q.acknowledgment != "" || q.ownership != "" || q.snooze != "") && q.kind != "platform_alert" ||
		q.acknowledgment != "" && q.acknowledgment != "acknowledged" && q.acknowledgment != "unacknowledged" ||
		q.ownership != "" && q.ownership != "owned" && q.ownership != "unowned" && q.ownership != "mine" ||
		q.snooze != "" && q.snooze != "active" && q.snooze != "inactive" {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid triage filter", "type=platform_alert required; acknowledgment=acknowledged|unacknowledged, ownership=owned|unowned|mine, snooze=active|inactive")
	}
	return nil
}
