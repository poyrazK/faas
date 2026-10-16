package apidserver

// Unified operator incident inbox.
//
// GET /v1/admin/obs/incidents composes the existing bounded operator reads
// into one deterministic, read-only projection. The handler does not create a
// new incident table or write to deployment/job/node state; the source rows
// remain owned by their existing controllers.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	since        time.Time
	sinceClamped bool
	kind         string
	severity     string
	cursor       string
	limit        int
	beforeAt     time.Time
	beforeID     string
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
		floor := now.Add(-time.Duration(api.ObsAdminWindowMaxHours) * time.Hour)
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
			enrichObsIncident(&items[i])
		}
	}
	if q.kind == "" || q.kind == "platform_alert" {
		alerts, available := s.obsFiringAlertIncidents(r)
		if !available {
			unavailable = append(unavailable, "prometheus")
		}
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

func enrichObsIncident(item *api.ObsIncident) {
	item.DedupeKey = strings.Replace(item.ID, "/", ":", 1)
	if item.AccountID != "" {
		item.AuditPath = "/v1/admin/obs/audit-log/search?target_account_id=" + item.AccountID
	}
	switch item.Type {
	case "deployment":
		item.ActionPath = "/v1/admin/ops/deployments/" + item.ResourceID
		item.RunbookURL = "/docs/runbooks/FaasDeployFailed.md"
	case "job_run":
		item.ActionPath = "/v1/admin/ops/jobs/runs/" + item.ResourceID
		item.RunbookURL = "/docs/ops/gregalectl-operator-quickstart.md#job-run-incidents"
	case "compute_node":
		item.ActionPath = "/v1/admin/obs/nodes/" + item.ResourceName + "/detail"
		item.AuditPath = "/v1/admin/obs/audit-log/search?kind_prefix=operator.action."
		item.RunbookURL = "/docs/runbooks/FaasComputeNodeStuckInactive.md"
	}
}

func (s *server) obsFiringAlertIncidents(r *http.Request) ([]api.ObsIncident, bool) {
	if s.promqlClient == nil {
		return nil, false
	}
	// ALERTS_FOR_STATE holds the persisted activation time. A scrape timestamp
	// changes on every request and would make keyset pagination skip alerts.
	rows, err := s.promqlClient.QueryVector(r.Context(), `ALERTS{alertstate="firing",alertname!="FaasWatchdog",severity!="info"} * ignoring(alertstate) group_left() ALERTS_FOR_STATE`)
	if err != nil {
		return nil, false
	}
	out := make([]api.ObsIncident, 0, len(rows))
	for _, row := range rows {
		name := row.Labels["alertname"]
		if name == "" || row.Value <= 0 {
			return nil, false
		}
		severity := "warning"
		switch row.Labels["severity"] {
		case "page", "critical":
			severity = "critical"
		case "error":
			severity = "error"
		}
		labels, _ := json.Marshal(row.Labels)
		key := fmt.Sprintf("platform_alert/%x", sha256.Sum256(labels))
		summary := name
		if component := row.Labels["component"]; component != "" {
			summary += " (" + component + ")"
		}
		out = append(out, api.ObsIncident{ID: key, Type: "platform_alert", Severity: severity, Status: "firing", Summary: summary, ResourceID: name, ResourceName: summary, ObservedAt: time.UnixMilli(int64(row.Value * 1000)).UTC(), DedupeKey: key, ActionPath: "/v1/admin/obs/health", RunbookURL: "/docs/ops/alert-cookbook.md"})
	}
	return out, true
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
